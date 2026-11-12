// Command lorahub-sat is the web page of a satellite Bobcat: telemetry and
// network settings (wired, Wi-Fi, an access point bridged to the cable, and
// where the hub is). Sign in with the Bobcat's root password.
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
	"syscall"
	"time"

	"github.com/nor24o/lorahub/internal/satpanel"
)

var version = "dev"

func main() {
	listen := flag.String("listen", ":80", "address to serve the page on")
	loraConfig := flag.String("lora-config", "/etc/lora/config.json", "the radio engine's config.json")
	eth := flag.String("ethernet", "end0", "wired network device")
	wifi := flag.String("wifi", "wlan0", "Wi-Fi device")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	panel := satpanel.New(satpanel.Options{Version: version, LoraConfig: *loraConfig, Ethernet: *eth, WiFi: *wifi, Log: log})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go panel.Run(ctx)

	srv := &http.Server{Addr: *listen, Handler: panel.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Info("satellite page", "version", version, "listen", *listen)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("cannot serve the page", "err", err)
		os.Exit(1)
	}
}
