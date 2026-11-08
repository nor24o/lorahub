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
		for i, suffix := range []string{"push", "replace"} {
			replace := suffix == "replace"
			srv.Subscribe(prefix+"/+/devices/+/down/"+suffix, i+1, func(cl *mqtt.Client, sub packets.Subscription, pk packets.Packet) {
				m.handleDown(pk.TopicName, pk.Payload, replace)
			})
		}
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
			c.Subscribe(prefix+"/+/devices/+/down/push", 0, func(_ paho.Client, msg paho.Message) { m.handleDown(msg.Topic(), msg.Payload(), false) })
			c.Subscribe(prefix+"/+/devices/+/down/replace", 0, func(_ paho.Client, msg paho.Message) { m.handleDown(msg.Topic(), msg.Payload(), true) })
		})
	c := paho.NewClient(o)
	c.Connect()
	m.ext = c
	return c
}

func (m *MQTT) handleDown(topic string, payload []byte, replace bool) {
	parts := strings.Split(topic, "/")
	if len(parts) < 6 {
		return
	}
	appID, devID := parts[1], parts[3]
	var req struct {
		Downlinks []struct {
			FPort      int    `json:"f_port"`
			FRMPayload string `json:"frm_payload"`
			Hex        string `json:"frm_payload_hex"`
			Decoded    any    `json:"decoded_payload"`
			Confirmed  bool   `json:"confirmed"`
		} `json:"downlinks"`
	}
	if err := json.Unmarshal(payload, &req); err != nil {
		m.log.Warn("mqtt downlink: bad json", "topic", topic, "err", err)
		return
	}
	for i, d := range req.Downlinks {
		var b []byte
		if d.FRMPayload != "" {
			b, _ = base64.StdEncoding.DecodeString(d.FRMPayload)
		} else if d.Hex != "" {
			b, _ = hexDecode(d.Hex)
		}
		if err := m.downlink(appID, devID, d.FPort, b, d.Decoded, d.Confirmed, replace && i == 0); err != nil {
			m.log.Warn("mqtt downlink", "app", appID, "dev", devID, "err", err)
		}
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
		m.publish(fmt.Sprintf("%s/%s/devices/%s/up", p, e.AppID, e.DevID), b, false)
		if s.HomeAssistant.Enabled {
			m.haDiscovery(e, s)
		}
	case e.Name == "ns.join.accept":
		m.publish(fmt.Sprintf("%s/%s/devices/%s/join", p, e.AppID, e.DevID), b, false)
	case e.Name == "as.down.queued":
		m.publish(fmt.Sprintf("%s/%s/devices/%s/down/queued", p, e.AppID, e.DevID), b, false)
	case e.Name == "ns.down.sent":
		m.publish(fmt.Sprintf("%s/%s/devices/%s/down/sent", p, e.AppID, e.DevID), b, false)
	case e.Name == "ns.down.failed":
		m.publish(fmt.Sprintf("%s/%s/devices/%s/down/failed", p, e.AppID, e.DevID), b, false)
	case e.Name == "ns.up.unmatched":
		m.publish("lorahub/unmatched", b, false)
	case e.Name == "gs.up" && len(e.Gateways) > 0:
		m.publish("lorahub/gateways/"+e.Gateways[0]+"/up", b, false)
	case e.Name == "gs.status" && len(e.Gateways) > 0:
		m.publish("lorahub/gateways/"+e.Gateways[0]+"/status", b, true)
	}
}

func (m *MQTT) haDiscovery(e *model.Event, s config.Settings) {
	msg, ok := e.Data.(map[string]any)
	if !ok {
		return
	}
	up, _ := msg["uplink_message"].(map[string]any)
	dec, _ := up["decoded_payload"].(map[string]any)
	stateTopic := fmt.Sprintf("%s/%s/devices/%s/up", s.MQTTTopicPrefix, e.AppID, e.DevID)
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
			"device": map[string]any{"identifiers": []string{sanitize(devIdent)}, "name": e.DevID,
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
			emit(k, fmt.Sprintf("{{ value_json.uplink_message.decoded_payload['%s'] }}", k), "")
		case map[string]any:
			for k2, v2 := range vv {
				switch v2.(type) {
				case float64, int64, int:
					emit(k+"_"+k2, fmt.Sprintf("{{ value_json.uplink_message.decoded_payload['%s']['%s'] }}", k, k2), "")
				}
			}
		}
	}
	emit("rssi", "{{ value_json.uplink_message.rx_metadata[0].rssi }}", "dBm")
	emit("snr", "{{ value_json.uplink_message.rx_metadata[0].snr }}", "dB")
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
