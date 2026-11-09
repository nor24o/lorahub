// Command lorahub is a standalone LoRa / LoRaWAN gateway processor for the
// Bobcat Miner 300 (and any Linux box): it ingests packets from the local
// SX1302 engine and from satellite gateways, decodes them and exposes them
// through a web console, MQTT, webhooks and a REST API.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nor24o/lorahub/internal/api"
	"github.com/nor24o/lorahub/internal/config"
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

	h, err := startHub(cfg, log, 20*time.Second)
	if err != nil {
		log.Error("start", "err", err)
		os.Exit(1)
	}
	log.Info("web console listening", "addr", h.httpAddr, "version", version)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Info("shutting down")
	h.Close()
}
