package integration

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/events"
	"github.com/nor24o/lorahub/internal/model"
	"github.com/nor24o/lorahub/internal/store"
)

var webhookEvents = map[string]string{
	"as.up":          "up",
	"ns.join.accept": "join",
	"as.down.queued": "downlink_queued",
	"ns.down.sent":   "downlink_sent",
	"ns.down.failed": "downlink_failed",
}

// Webhooks posts application events to configured URLs.
type Webhooks struct {
	store  *store.Store
	log    *slog.Logger
	client *http.Client
	sem    chan struct{}
	report func(*model.Event)
}

// NewWebhooks creates the webhook dispatcher. report is used to emit
// delivery failures as events.
func NewWebhooks(st *store.Store, log *slog.Logger, report func(*model.Event)) *Webhooks {
	return &Webhooks{store: st, log: log, client: &http.Client{Timeout: 10 * time.Second}, sem: make(chan struct{}, 8), report: report}
}

// Run consumes events.
func (w *Webhooks) Run(bus *events.Bus, stop <-chan struct{}) {
	ch, unsub := bus.Subscribe(1024, func(e *model.Event) bool { _, ok := webhookEvents[e.Name]; return ok && e.AppID != "" })
	defer unsub()
	for {
		select {
		case <-stop:
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			app, err := w.store.Application(e.AppID)
			if err != nil {
				continue
			}
			kind := webhookEvents[e.Name]
			for _, h := range app.Webhooks {
				if !h.Enabled || h.URL == "" || !contains(h.Events, kind) {
					continue
				}
				hook := h
				ev := e
				w.sem <- struct{}{}
				go func() {
					defer func() { <-w.sem }()
					w.deliver(app.ID, hook, kind, ev)
				}()
			}
		}
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func (w *Webhooks) deliver(appID string, h model.Webhook, kind string, e *model.Event) {
	var body any = e.Data
	if h.Format == "decoded" && kind == "up" {
		if m, ok := e.Data.(map[string]any); ok {
			up, _ := m["uplink_message"].(map[string]any)
			body = map[string]any{"application_id": appID, "device_id": e.DevID, "received_at": m["received_at"],
				"decoded_payload": up["decoded_payload"], "f_port": up["f_port"], "f_cnt": up["f_cnt"],
				"frm_payload_hex": up["frm_payload_hex"], "rx_metadata": up["rx_metadata"]}
		}
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequest(http.MethodPost, h.URL, bytes.NewReader(b))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "LoRaHub-Webhook")
	req.Header.Set("X-LoRaHub-Event", kind)
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := w.client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return
			}
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			if resp.StatusCode < 500 {
				break
			}
		} else {
			lastErr = err
		}
		time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
		req.Body = ioNopCloser(b)
	}
	w.log.Warn("webhook delivery failed", "app", appID, "webhook", h.Name, "err", lastErr)
	if w.report != nil {
		w.report(&model.Event{Name: "as.webhook.failed", AppID: appID, DevID: e.DevID,
			Summary: fmt.Sprintf("Webhook %q failed: %v", h.Name, lastErr), Data: map[string]any{"webhook": h.Name, "url": h.URL, "error": fmt.Sprint(lastErr)}})
	}
}

type nopCloser struct{ *bytes.Reader }

func (nopCloser) Close() error { return nil }

func ioNopCloser(b []byte) nopCloser { return nopCloser{bytes.NewReader(b)} }

func hexDecode(s string) ([]byte, error) {
	return hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
}
