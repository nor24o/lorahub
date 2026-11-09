// Command lorahub is a standalone LoRa / LoRaWAN gateway processor for the
// Bobcat Miner 300 (and any Linux box): it ingests packets from the local
// SX1302 engine and from satellite gateways, decodes them and exposes them
// through a web console, MQTT, webhooks and a REST API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/nor24o/lorahub/internal/api"
	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/events"
	"github.com/nor24o/lorahub/internal/ingest"
	"github.com/nor24o/lorahub/internal/integration"
	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/processor"
	"github.com/nor24o/lorahub/internal/store"
	"github.com/nor24o/lorahub/web"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/etc/lorahub/lorahub.yml", "configuration file")
	dataDir := flag.String("data", "", "override data directory")
	listen := flag.String("listen", "", "override HTTP listen address")
	debug := flag.Bool("debug", false, "debug logging")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	lvl := slog.LevelInfo
	if *debug {
		lvl = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl}))
	api.Version = version

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}
	if *dataDir != "" {
		cfg.DataDir = *dataDir
	}
	if *listen != "" {
		cfg.HTTP.Listen = *listen
	}

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Error("database", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	bus := events.New()
	proc := processor.New(st, bus, log)
	if ok, err := proc.ImportSchemesFile("/etc/lora/decoders.json"); err != nil {
		log.Warn("import /etc/lora/decoders.json", "err", err)
	} else if ok {
		log.Info("imported decoder schemes from /etc/lora/decoders.json")
	}
	stop := make(chan struct{})

	if cfg.SemtechUDP.Enabled {
		srv, err := ingest.NewSemtechServer(cfg.SemtechUDP.Listen, proc, log)
		if err != nil {
			log.Error("semtech udp listener", "err", err)
			os.Exit(1)
		}
		defer srv.Close()
		proc.RegisterDownlinker("semtech_udp", srv)
		log.Info("Semtech UDP (satellite gateways) listening", "addr", cfg.SemtechUDP.Listen)
	}
	if cfg.Bobcat.Enabled {
		resolve := func(ip string) string {
			for _, g := range st.Gateways() {
				if g.Kind == "bobcat" && (g.LastAddr == ip || strings.HasPrefix(g.LastAddr, ip+":")) {
					return g.EUI
				}
			}
			return ""
		}
		srv, err := ingest.NewBobcatServer(cfg.Bobcat.Listen, cfg.Bobcat.DownlinkPort, cfg.Bobcat.StaticEUIs, resolve, proc, log)
		if err != nil {
			log.Error("bobcat raw listener", "err", err)
			os.Exit(1)
		}
		srv.ResolveAddr = func(eui string) string {
			if g, err := st.GatewayByEUI(eui); err == nil && g.Kind == "bobcat" {
				return g.LastAddr
			}
			return ""
		}
		srv.LinkKey = func() string { return proc.Settings().LinkKey }
		srv.AutoPair = func() bool { return proc.Settings().LinkAutoPair }
		srv.ResolvePort = func(eui string) int {
			if g, err := st.GatewayByEUI(eui); err == nil {
				return g.ControlPort
			}
			return 0
		}
		defer srv.Close()
		proc.RegisterDownlinker("bobcat", srv)
		log.Info("raw_lora_app (Bobcat) listener", "addr", cfg.Bobcat.Listen, "downlink_port", cfg.Bobcat.DownlinkPort)
	}
	proc.ReloadForwarders()

	mq, err := integration.NewMQTT(cfg, proc.Settings, func(app, dev string, fport int, payload []byte, decoded any, confirmed, replace bool) error {
		_, err := proc.EnqueueDownlink(app, dev, fport, payload, decoded, confirmed, replace)
		return err
	}, log)
	if err != nil {
		log.Error("mqtt", "err", err)
		os.Exit(1)
	}
	defer mq.Close()
	mq.Device = st.Device
	mq.Devices = func() []*model.Device { return st.Devices("") }
	mq.Gateways = func() []string {
		var ids []string
		for _, g := range st.Gateways() {
			ids = append(ids, g.ID)
		}
		return ids
	}
	go mq.Run(bus, stop)
	for _, id := range proc.RenamedGateways() {
		// Later, so an external broker has had time to connect.
		time.AfterFunc(20*time.Second, func() { mq.ForgetGateway(id) })
	}

	report := func(e *model.Event) {
		e.Time = time.Now().UTC()
		st.AddEvent(e)
		bus.Publish(e)
	}
	go integration.NewWebhooks(st, log, report).Run(bus, stop)
	go proc.Housekeeping(stop)

	srv := &api.Server{Cfg: cfg, Store: st, Bus: bus, Proc: proc, MQTT: mq, Log: log, Web: web.FS()}
	go srv.ProbeControlPorts(stop)
	httpSrv := &http.Server{Addr: cfg.HTTP.Listen, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Info("web console listening", "addr", cfg.HTTP.Listen, "version", version)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", "err", err)
			os.Exit(1)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info("shutting down")
	close(stop)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	httpSrv.Shutdown(ctx)
	st.FlushGateways()
}
