package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/api"
	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/events"
	"github.com/nor24o/lorahub/internal/ingest"
	"github.com/nor24o/lorahub/internal/integration"
	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/processor"
	"github.com/nor24o/lorahub/internal/satpanel"
	"github.com/nor24o/lorahub/internal/store"
	"github.com/nor24o/lorahub/web"
)

// hub is a running LoRaHub.
type hub struct {
	store    *store.Store
	proc     *processor.Processor
	mqtt     *integration.MQTT
	bobcat   *ingest.BobcatServer
	httpAddr string

	stop    chan struct{}
	httpSrv *http.Server
	closers []func()
}

// startHub wires LoRaHub together from cfg and starts it. announceAfter is
// how long it waits before announcing everything to Home Assistant again.
func startHub(cfg *config.Config, log *slog.Logger, announceAfter time.Duration) (*hub, error) {
	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return nil, err
	}
	h := &hub{store: st, stop: make(chan struct{})}
	h.closers = append(h.closers, func() { st.Close() })
	fail := func(err error) (*hub, error) {
		h.Close()
		return nil, err
	}

	bus := events.New()
	proc := processor.New(st, bus, log)
	h.proc = proc
	if ok, err := proc.ImportSchemesFile("/etc/lora/decoders.json"); err != nil {
		log.Warn("import /etc/lora/decoders.json", "err", err)
	} else if ok {
		log.Info("imported decoder schemes from /etc/lora/decoders.json")
	}

	if cfg.SemtechUDP.Enabled {
		srv, err := ingest.NewSemtechServer(cfg.SemtechUDP.Listen, proc, log)
		if err != nil {
			return fail(err)
		}
		h.closers = append(h.closers, func() { srv.Close() })
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
			return fail(err)
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
		h.bobcat = srv
		h.closers = append(h.closers, func() { srv.Close() })
		proc.RegisterDownlinker("bobcat", srv)
		log.Info("raw_lora_app (Bobcat) listener", "addr", srv.LocalAddr().String(), "downlink_port", cfg.Bobcat.DownlinkPort)
	}
	proc.ReloadForwarders()

	mq, err := integration.NewMQTT(cfg, proc.Settings, func(app, dev string, fport int, payload []byte, decoded any, confirmed, replace bool) error {
		_, err := proc.EnqueueDownlink(app, dev, fport, payload, decoded, confirmed, replace)
		return err
	}, log)
	if err != nil {
		return fail(err)
	}
	h.mqtt = mq
	h.closers = append(h.closers, mq.Close)
	mq.Device = st.Device
	mq.Devices = func() []*model.Device { return st.Devices("") }
	mq.Gateways = func() []string {
		var ids []string
		for _, g := range st.Gateways() {
			ids = append(ids, g.ID)
		}
		return ids
	}
	mq.LastMessage = func(app, dev string) []byte {
		evs, err := st.Events(store.EventFilter{AppID: app, DevID: dev, Names: []string{"as.up"}, Limit: 20})
		if err != nil {
			return nil
		}
		for _, e := range evs {
			if m, ok := e.Data.(map[string]any); ok && m["simulated"] == true {
				continue // test packets never become a node's values
			}
			b, _ := json.Marshal(e.Data)
			return b
		}
		return nil
	}
	mq.Group = st.Application
	mq.SaveControlValue = func(app, dev, key, value string) {
		st.UpdateDevice(app, dev, func(d *model.Device) error {
			if d.HAValues == nil {
				d.HAValues = map[string]string{}
			}
			d.HAValues[key] = value
			return nil
		})
	}
	proc.NodeChanged = mq.NodeChanged // a node's own description changes its controls
	go mq.Run(bus, h.stop)
	// Announce again once brokers are connected, so a restart leaves no entity
	// unknown until its node next transmits.
	t := time.AfterFunc(announceAfter, mq.RepublishHA)
	h.closers = append(h.closers, func() { t.Stop() })
	for _, id := range proc.RenamedGateways() {
		// Later, so an external broker has had time to connect.
		time.AfterFunc(announceAfter, func() { mq.ForgetGateway(id) })
	}

	report := func(e *model.Event) {
		e.Time = time.Now().UTC()
		st.AddEvent(e)
		bus.Publish(e)
	}
	go integration.NewWebhooks(st, log, report).Run(bus, h.stop)
	go proc.Housekeeping(h.stop)

	// The board's own settings (network, Wi-Fi, SD card, lights): the same code
	// as a satellite's page, checked against the home router instead of the hub.
	host := satpanel.New(satpanel.Options{Role: satpanel.RoleHub, Version: version, Log: log.With("part", "device"), Backup: st.Backup})
	if runtime.GOOS == "linux" {
		ctx, cancel := context.WithCancel(context.Background())
		h.closers = append(h.closers, cancel)
		go host.Run(ctx)
	}
	srv := &api.Server{Cfg: cfg, Store: st, Bus: bus, Proc: proc, MQTT: mq, Log: log, Web: web.FS(), Host: host.API()}
	go srv.ProbeControlPorts(h.stop)
	ln, err := net.Listen("tcp", cfg.HTTP.Listen)
	if err != nil {
		return fail(err)
	}
	h.httpAddr = ln.Addr().String()
	h.httpSrv = &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := h.httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http", "err", err)
		}
	}()
	return h, nil
}

// Close stops everything startHub started.
func (h *hub) Close() {
	select {
	case <-h.stop:
		return
	default:
		close(h.stop)
	}
	if h.httpSrv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		h.httpSrv.Shutdown(ctx)
		cancel()
	}
	if h.store != nil {
		h.store.FlushGateways()
	}
	for i := len(h.closers) - 1; i >= 0; i-- {
		h.closers[i]()
	}
}
