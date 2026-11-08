// Package integration publishes LoRaHub events to MQTT (embedded broker
// and/or an external broker), Home Assistant discovery and HTTP webhooks,
// and accepts downlinks over MQTT.
package integration

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/mochi-mqtt/server/v2/packets"

	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/events"
	"github.com/nor24o/lorahub/internal/model"
)

// DownlinkFunc enqueues a downlink.
type DownlinkFunc func(appID, devID string, fport int, payload []byte, decoded any, confirmed, replace bool) error

// MQTT manages the embedded broker and the external client.
type MQTT struct {
	log      *slog.Logger
	broker   *mqtt.Server
	settings func() config.Settings
	downlink DownlinkFunc

	mu     sync.Mutex
	ext    paho.Client
	extKey string
	haSent map[string]bool
}

// NewMQTT creates the MQTT integration; the embedded broker is started if
// enabled in cfg.
func NewMQTT(cfg *config.Config, settings func() config.Settings, dl DownlinkFunc, log *slog.Logger) (*MQTT, error) {
	m := &MQTT{log: log, settings: settings, downlink: dl, haSent: map[string]bool{}}
	if cfg.MQTTBroker.Enabled {
		srv := mqtt.New(&mqtt.Options{InlineClient: true})
		if cfg.MQTTBroker.Username != "" {
			err := srv.AddHook(new(auth.Hook), &auth.Options{Ledger: &auth.Ledger{
				Auth: auth.AuthRules{{Username: auth.RString(cfg.MQTTBroker.Username), Password: auth.RString(cfg.MQTTBroker.Password), Allow: true},
					{Remote: "127.0.0.1:*", Allow: true}},
			}})
			if err != nil {
				return nil, err
			}
		} else {
			srv.AddHook(new(auth.AllowHook), nil)
		}
		if err := srv.AddListener(listeners.NewTCP(listeners.Config{ID: "tcp", Address: cfg.MQTTBroker.Listen})); err != nil {
			return nil, err
		}
		if cfg.MQTTBroker.WSListen != "" {
			if err := srv.AddListener(listeners.NewWebsocket(listeners.Config{ID: "ws", Address: cfg.MQTTBroker.WSListen})); err != nil {
				return nil, err
			}
		}
		go func() {
			if err := srv.Serve(); err != nil {
				log.Error("mqtt broker", "err", err)
			}
		}()
		m.broker = srv
		prefix := settings().MQTTTopicPrefix
		srv.Subscribe(prefix+"/+/+/down", 1, func(cl *mqtt.Client, sub packets.Subscription, pk packets.Packet) {
			m.handleDown(pk.TopicName, pk.Payload)
		})
		log.Info("embedded MQTT broker listening", "addr", cfg.MQTTBroker.Listen)
	}
	return m, nil
}

// BrokerClients returns the number of connected clients of the embedded broker.
func (m *MQTT) BrokerClients() int {
	if m.broker == nil {
		return 0
	}
	n := 0
	for _, c := range m.broker.Clients.GetAll() {
		if !c.Closed() && c.ID != mqtt.InlineClientId {
			n++
		}
	}
	return n
}

// ExternalConnected reports the external broker connection state.
func (m *MQTT) ExternalConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ext != nil && m.ext.IsConnected()
}

func (m *MQTT) ensureExternal() paho.Client {
	s := m.settings().ExternalMQTT
	key := fmt.Sprintf("%v|%s|%s|%s|%s", s.Enabled, s.URL, s.Username, s.Password, s.ClientID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if key == m.extKey {
		return m.ext
	}
	if m.ext != nil {
		m.ext.Disconnect(250)
		m.ext = nil
	}
	m.extKey = key
	if !s.Enabled || s.URL == "" {
		return nil
	}
	cid := s.ClientID
	if cid == "" {
		cid = "lorahub-" + fmt.Sprint(time.Now().Unix())
	}
	prefix := m.settings().MQTTTopicPrefix
	o := paho.NewClientOptions().AddBroker(s.URL).SetClientID(cid).SetUsername(s.Username).SetPassword(s.Password).
		SetAutoReconnect(true).SetConnectRetry(true).SetConnectRetryInterval(10 * time.Second).
		SetOnConnectHandler(func(c paho.Client) {
			m.log.Info("external MQTT connected", "url", s.URL)
			c.Subscribe(prefix+"/+/+/down", 0, func(_ paho.Client, msg paho.Message) { m.handleDown(msg.Topic(), msg.Payload()) })
		})
	c := paho.NewClient(o)
	c.Connect()
	m.ext = c
	return c
}

// handleDown accepts downlinks on <prefix>/<group>/<node>/down with body
// {"data_hex":"0102"} | {"data":"<base64>"} | {"decoded":{...}}, optional
// "f_port", "confirmed", "replace".
func (m *MQTT) handleDown(topic string, payload []byte) {
	parts := strings.Split(topic, "/")
	if len(parts) != 4 {
		return
	}
	appID, devID := parts[1], parts[2]
	var req struct {
		DataHex   string `json:"data_hex"`
		Data      string `json:"data"`
		Decoded   any    `json:"decoded"`
		FPort     int    `json:"f_port"`
		Confirmed bool   `json:"confirmed"`
		Replace   bool   `json:"replace"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		m.log.Warn("mqtt downlink: bad json", "topic", topic, "err", err)
		return
	}
	var b []byte
	if req.DataHex != "" {
		b, _ = hexDecode(req.DataHex)
	} else if req.Data != "" {
		b, _ = base64.StdEncoding.DecodeString(req.Data)
	}
	if req.FPort == 0 {
		req.FPort = 1
	}
	if err := m.downlink(appID, devID, req.FPort, b, req.Decoded, req.Confirmed, req.Replace); err != nil {
		m.log.Warn("mqtt downlink", "group", appID, "node", devID, "err", err)
	}
}

func (m *MQTT) publish(topic string, payload []byte, retain bool) {
	if m.broker != nil {
		m.broker.Publish(topic, payload, retain, 0)
	}
	if c := m.ensureExternal(); c != nil && c.IsConnected() {
		c.Publish(topic, 0, retain, payload)
	}
}

// Run consumes events from the bus and publishes them.
func (m *MQTT) Run(bus *events.Bus, stop <-chan struct{}) {
	ch, unsub := bus.Subscribe(1024, nil)
	defer unsub()
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
			m.ensureExternal()
		case e, ok := <-ch:
			if !ok {
				return
			}
			m.handleEvent(e)
		}
	}
}

func (m *MQTT) handleEvent(e *model.Event) {
	s := m.settings()
	p := s.MQTTTopicPrefix
	b, _ := json.Marshal(e.Data)
	switch {
	case e.Name == "as.up":
		m.publish(fmt.Sprintf("%s/%s/%s/up", p, e.AppID, e.DevID), b, false)
		if s.HomeAssistant.Enabled {
			m.haDiscovery(e, s)
		}
	case e.Name == "ns.join.accept":
		m.publish(fmt.Sprintf("%s/%s/%s/join", p, e.AppID, e.DevID), b, false)
	case e.Name == "as.down.queued":
		m.publish(fmt.Sprintf("%s/%s/%s/down/queued", p, e.AppID, e.DevID), b, false)
	case e.Name == "ns.down.sent":
		m.publish(fmt.Sprintf("%s/%s/%s/down/sent", p, e.AppID, e.DevID), b, false)
	case e.Name == "ns.down.failed":
		m.publish(fmt.Sprintf("%s/%s/%s/down/failed", p, e.AppID, e.DevID), b, false)
	case e.Name == "ns.up.unmatched":
		m.publish(p+"/unmatched", b, false)
	case e.Name == "gs.up" && len(e.Gateways) > 0:
		m.publish(p+"/gateways/"+e.Gateways[0]+"/up", b, false)
	case e.Name == "gs.status" && len(e.Gateways) > 0:
		m.publish(p+"/gateways/"+e.Gateways[0]+"/status", b, true)
	}
}

func (m *MQTT) haDiscovery(e *model.Event, s config.Settings) {
	msg, ok := e.Data.(map[string]any)
	if !ok {
		return
	}
	dec, _ := msg["decoded"].(map[string]any)
	stateTopic := fmt.Sprintf("%s/%s/%s/up", s.MQTTTopicPrefix, e.AppID, e.DevID)
	devIdent := "lorahub_" + e.AppID + "_" + e.DevID
	emit := func(field, tpl string, unitGuess string) {
		key := devIdent + "_" + field
		m.mu.Lock()
		done := m.haSent[key]
		m.haSent[key] = true
		m.mu.Unlock()
		if done {
			return
		}
		cfg := map[string]any{
			"name":           field,
			"unique_id":      sanitize(key),
			"object_id":      sanitize(key),
			"state_topic":    stateTopic,
			"value_template": tpl,
			"device": map[string]any{"identifiers": []string{sanitize(devIdent)}, "name": fmt.Sprint(msg["name"]),
				"manufacturer": "LoRaHub", "model": e.AppID},
		}
		if dc, unit := haClass(field); dc != "" {
			cfg["device_class"] = dc
			cfg["unit_of_measurement"] = unit
			cfg["state_class"] = "measurement"
		} else if unitGuess != "" {
			cfg["unit_of_measurement"] = unitGuess
		}
		b, _ := json.Marshal(cfg)
		m.publish(fmt.Sprintf("%s/sensor/%s/config", s.HomeAssistant.DiscoveryPrefix, sanitize(key)), b, true)
	}
	for k, v := range dec {
		switch vv := v.(type) {
		case float64, int64, int, bool, string:
			_ = vv
			emit(k, fmt.Sprintf("{{ value_json.decoded['%s'] }}", k), "")
		case map[string]any:
			for k2, v2 := range vv {
				switch v2.(type) {
				case float64, int64, int:
					emit(k+"_"+k2, fmt.Sprintf("{{ value_json.decoded['%s']['%s'] }}", k, k2), "")
				}
			}
		}
	}
	emit("rssi", "{{ value_json.gateways[0].rssi }}", "dBm")
	emit("snr", "{{ value_json.gateways[0].snr }}", "dB")
}

func haClass(field string) (string, string) {
	f := strings.ToLower(field)
	switch {
	case strings.Contains(f, "temp"):
		return "temperature", "°C"
	case strings.Contains(f, "humid"):
		return "humidity", "%"
	case strings.Contains(f, "batt") && strings.Contains(f, "%"), strings.Contains(f, "battery_percent"):
		return "battery", "%"
	case strings.Contains(f, "volt"), f == "battery", strings.HasPrefix(f, "vbat"):
		return "voltage", "V"
	case strings.Contains(f, "pressure"):
		return "pressure", "hPa"
	case strings.Contains(f, "illum"), strings.Contains(f, "lux"):
		return "illuminance", "lx"
	case f == "rssi":
		return "signal_strength", "dBm"
	}
	return "", ""
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			return r
		}
		return '_'
	}, s)
}

// Close shuts down the broker and client.
func (m *MQTT) Close() {
	if m.broker != nil {
		m.broker.Close()
	}
	m.mu.Lock()
	if m.ext != nil {
		m.ext.Disconnect(250)
	}
	m.mu.Unlock()
}
