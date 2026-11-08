package integration

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/model"
)

// handleSet accepts simple commands on <prefix>/<group>/<node>/set:
// ON, OFF, 1, 0 or a hex payload.
func (m *MQTT) handleSet(appID, devID string, payload []byte) {
	cmd := strings.TrimSpace(string(payload))
	var b []byte
	switch strings.ToUpper(cmd) {
	case "ON", "1":
		b = []byte{0x01, 0x01}
	case "OFF", "0":
		b = []byte{0x01, 0x00}
	default:
		var err error
		if b, err = hexDecode(cmd); err != nil || len(b) == 0 {
			m.log.Warn("mqtt set: expected ON, OFF or hex", "group", appID, "node", devID)
			return
		}
	}
	if err := m.downlink(appID, devID, 1, b, nil, false, false); err != nil {
		m.log.Warn("mqtt set", "group", appID, "node", devID, "err", err)
	}
}

// nodeState publishes the flat retained state of a node and keeps its Home
// Assistant entities up to date. raw is the JSON of the as.up message.
func (m *MQTT) nodeState(e *model.Event, s config.Settings, raw []byte) {
	var msg struct {
		Decoded      map[string]any   `json:"decoded"`
		Gateways     []map[string]any `json:"gateways"`
		Radio        map[string]any   `json:"radio"`
		PayloadHex   string           `json:"payload_hex"`
		Size         int              `json:"size"`
		GatewayCount int              `json:"gateway_count"`
		ReceivedAt   string           `json:"received_at"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return
	}
	state := map[string]any{}
	for k, v := range flatten(msg.Decoded) {
		state[k] = v
	}
	state["hex"], state["size"], state["gateway_count"], state["last_seen"] = msg.PayloadHex, msg.Size, msg.GatewayCount, msg.ReceivedAt
	gateway := ""
	if len(msg.Gateways) > 0 {
		state["rssi"], state["snr"] = msg.Gateways[0]["rssi"], msg.Gateways[0]["snr"]
		gateway, _ = msg.Gateways[0]["gateway_id"].(string)
		state["gateway"] = gateway
	}
	if f, ok := msg.Radio["frequency"].(float64); ok {
		state["freq"] = f / 1e6
	}
	state["datr"] = msg.Radio["data_rate"]
	b, _ := json.Marshal(state)
	m.publish(fmt.Sprintf("%s/%s/%s/state", s.MQTTTopicPrefix, e.AppID, e.DevID), b, true)

	if !s.HomeAssistant.Enabled || m.Device == nil || len(msg.Decoded) == 0 {
		return
	}
	d, err := m.Device(e.AppID, e.DevID)
	if err != nil || !exposed(d) {
		return
	}
	m.publishEntities(d, msg.Decoded, gateway, s)
}

func exposed(d *model.Device) bool { return d != nil && d.Adopted && d.HAEnabled }

func flatten(dec map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range dec {
		switch x := v.(type) {
		case map[string]any:
			for k2, v2 := range x {
				switch v2.(type) {
				case map[string]any, []any:
				default:
					out[k+"_"+k2] = v2
				}
			}
		case []any:
		default:
			out[k] = v
		}
	}
	return out
}

type haEntity struct {
	component, key string
	cfg            map[string]any
}

var channelState = regexp.MustCompile(`^ch\d+_(state|digital_in|val|analog_in|temperature|humidity)$`)

// entities lists a node's Home Assistant entities. The result depends only on
// the node and its field names, so the same call can also retract them.
func entities(d *model.Device, decoded map[string]any, s config.Settings) []haEntity {
	stateTopic := fmt.Sprintf("%s/%s/%s/state", s.MQTTTopicPrefix, d.AppID, d.ID)
	id := sanitize("lorahub_" + d.AppID + "_" + d.ID)
	var out []haEntity
	add := func(component, key, name, tpl string, extra map[string]any) {
		cfg := map[string]any{"name": name, "has_entity_name": true, "unique_id": sanitize(id + "_" + key),
			"state_topic": stateTopic, "value_template": tpl}
		for k, v := range extra {
			cfg[k] = v
		}
		out = append(out, haEntity{component, sanitize(key), cfg})
	}
	val := func(k string) string { return fmt.Sprintf("{{ value_json['%s'] | default(none) }}", k) }
	flat := flatten(decoded)
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	isSwitch := d.Class == "C" || strings.Contains(strings.ToLower(d.Model), "valve") ||
		strings.Contains(strings.ToLower(d.Model), "relay") || strings.Contains(strings.ToLower(d.Model), "actuator")
	_, hasState := flat["state"]
	cmd := fmt.Sprintf("%s/%s/%s/set", s.MQTTTopicPrefix, d.AppID, d.ID)
	for _, k := range keys {
		switch {
		case k == "node_class", k == "capability", k == "device_type", channelState.MatchString(k):
			continue
		case k == "seq":
			add("sensor", k, "Sequence", val(k), map[string]any{"entity_category": "diagnostic", "icon": "mdi:counter"})
		case k == "state" && isSwitch:
			continue // covered by the switch below
		case k == "state":
			add("binary_sensor", k, "State", "{{ value_json['state'] | default('OFF') }}", nil)
		default:
			if _, isBool := flat[k].(bool); isBool {
				add("binary_sensor", k, strings.ReplaceAll(k, "_", " "), fmt.Sprintf("{{ 'ON' if value_json['%s'] else 'OFF' }}", k), nil)
				continue
			}
			extra := map[string]any{}
			if dc, unit := haClass(k); dc != "" {
				extra["device_class"], extra["unit_of_measurement"], extra["state_class"] = dc, unit, "measurement"
			} else if _, num := flat[k].(float64); num {
				extra["state_class"] = "measurement"
			}
			add("sensor", k, strings.ReplaceAll(k, "_", " "), val(k), extra)
		}
	}
	if isSwitch {
		add("switch", "switch", "Output", "{{ value_json['state'] | default('OFF') }}", map[string]any{"command_topic": cmd,
			"payload_on": "ON", "payload_off": "OFF", "state_on": "ON", "state_off": "OFF", "optimistic": !hasState})
	}
	diag := func(extra map[string]any) map[string]any {
		extra["entity_category"] = "diagnostic"
		return extra
	}
	add("sensor", "rssi", "Signal RSSI", val("rssi"), diag(map[string]any{"device_class": "signal_strength", "unit_of_measurement": "dBm"}))
	add("sensor", "snr", "Signal SNR", val("snr"), diag(map[string]any{"unit_of_measurement": "dB", "icon": "mdi:signal-distance-variant"}))
	add("sensor", "freq", "Frequency", val("freq"), diag(map[string]any{"unit_of_measurement": "MHz", "icon": "mdi:radio-tower"}))
	add("sensor", "datr", "Data rate", val("datr"), diag(map[string]any{"icon": "mdi:antenna"}))
	add("sensor", "gateway_count", "Gateways", val("gateway_count"), diag(map[string]any{"icon": "mdi:access-point-network"}))
	add("sensor", "last_seen", "Last seen", val("last_seen"), diag(map[string]any{"device_class": "timestamp"}))
	return out
}

func entityTopic(s config.Settings, d *model.Device, e haEntity) string {
	return fmt.Sprintf("%s/%s/%s/%s/config", s.HomeAssistant.DiscoveryPrefix, e.component, sanitize("lorahub_"+d.AppID+"_"+d.ID), e.key)
}

func (m *MQTT) publishEntities(d *model.Device, decoded map[string]any, gateway string, s config.Settings) {
	id := sanitize("lorahub_" + d.AppID + "_" + d.ID)
	device := map[string]any{"identifiers": []string{id}, "name": d.Name, "manufacturer": "LoRaHub"}
	if d.Model != "" {
		device["model"] = d.Model
	} else {
		device["model"] = "LoRa node (class " + d.Class + ")"
	}
	if d.Area != "" {
		device["suggested_area"] = d.Area
	}
	if gateway != "" {
		device["via_device"] = sanitize("lorahub_gw_" + gateway)
	}
	for _, e := range entities(d, decoded, s) {
		topic := entityTopic(s, d, e)
		m.mu.Lock()
		done := m.haSent[topic]
		m.haSent[topic] = true
		m.mu.Unlock()
		if done {
			continue
		}
		// Retract the entity an earlier version published under another topic.
		m.publish(fmt.Sprintf("%s/sensor/%s/config", s.HomeAssistant.DiscoveryPrefix, sanitize(id+"_"+e.key)), []byte{}, true)
		e.cfg["device"] = device
		b, _ := json.Marshal(e.cfg)
		m.publish(topic, b, true)
	}
}

func normalize(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out map[string]any
	json.Unmarshal(b, &out)
	return out
}

// NodeChanged applies an edit or deletion (after == nil) to Home Assistant.
func (m *MQTT) NodeChanged(before, after *model.Device) {
	s := m.settings()
	if before != nil {
		wasOn := exposed(before)
		prefix := fmt.Sprintf("%s/", s.HomeAssistant.DiscoveryPrefix)
		marker := "/" + sanitize("lorahub_"+before.AppID+"_"+before.ID) + "/"
		m.mu.Lock()
		for t := range m.haSent {
			if strings.HasPrefix(t, prefix) && strings.Contains(t, marker) {
				delete(m.haSent, t)
			}
		}
		m.mu.Unlock()
		if wasOn && s.HomeAssistant.Enabled {
			for _, e := range entities(before, normalize(before.LastDecoded), s) {
				if after == nil || !exposed(after) {
					m.publish(entityTopic(s, before, e), []byte{}, true)
				}
			}
		}
	}
	if exposed(after) && s.HomeAssistant.Enabled && after.LastDecoded != nil {
		m.publishEntities(after, normalize(after.LastDecoded), after.LastGateway, s)
	}
}

// gatewayDiscovery exposes a gateway's counters, as the parent of its nodes.
func (m *MQTT) gatewayDiscovery(gatewayID string, data any, s config.Settings) {
	status := normalize(data)
	if status == nil {
		return
	}
	id := sanitize("lorahub_gw_" + gatewayID)
	device := map[string]any{"identifiers": []string{id}, "name": "LoRa gateway " + gatewayID, "manufacturer": "LoRaHub"}
	known := []struct{ key, name, class, unit string }{
		{"rx", "Packets received", "", ""}, {"tx", "Packets sent", "", ""}, {"crc_err", "CRC errors", "", ""}, {"tx_err", "TX errors", "", ""},
		{"uptime_s", "Uptime", "duration", "s"}, {"temp_c", "Concentrator temperature", "temperature", "°C"},
	}
	for _, k := range known {
		if _, ok := status[k.key]; !ok {
			continue
		}
		topic := fmt.Sprintf("%s/sensor/%s/%s/config", s.HomeAssistant.DiscoveryPrefix, id, k.key)
		m.mu.Lock()
		done := m.haSent[topic]
		m.haSent[topic] = true
		m.mu.Unlock()
		if done {
			continue
		}
		cfg := map[string]any{"name": k.name, "has_entity_name": true, "unique_id": id + "_" + k.key,
			"state_topic": s.MQTTTopicPrefix + "/gateways/" + gatewayID + "/status", "value_template": fmt.Sprintf("{{ value_json['%s'] | default(none) }}", k.key),
			"entity_category": "diagnostic", "device": device}
		if k.class != "" {
			cfg["device_class"], cfg["unit_of_measurement"] = k.class, k.unit
		}
		b, _ := json.Marshal(cfg)
		m.publish(topic, b, true)
	}
}
