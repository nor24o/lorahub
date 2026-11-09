package integration

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/model"
)

// handleSet accepts simple commands on <prefix>/<group>/<node>/set:
// ON, OFF, 1, 0 or a hex payload.
func (m *MQTT) handleSet(appID, devID string, payload []byte) {
	cmd := strings.TrimSpace(string(payload))
	cmds := m.settings().MQTTCommands
	switch strings.ToUpper(cmd) {
	case "ON", "1":
		cmd = cmds.OnHex
	case "OFF", "0":
		cmd = cmds.OffHex
	}
	b, err := hexDecode(cmd)
	if err != nil || len(b) == 0 {
		m.log.Warn("mqtt set: expected ON, OFF or hex", "group", appID, "node", devID)
		return
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
	// Home Assistant's node entities read this topic.
	if s.MQTTPublish.State || s.HomeAssistant.Enabled {
		m.publish(fmt.Sprintf("%s/%s/%s/state", s.MQTTTopicPrefix, e.AppID, e.DevID), b, s.MQTTPublish.StateRetain || s.HomeAssistant.Enabled)
	}

	if !s.HomeAssistant.Enabled || m.Device == nil || len(msg.Decoded) == 0 {
		return
	}
	d, err := m.Device(e.AppID, e.DevID)
	if err != nil || !exposed(d, s) {
		return
	}
	m.publishEntities(d, msg.Decoded, gateway, s)
}

// exposed reports whether a node goes to Home Assistant at all.
func exposed(d *model.Device, s config.Settings) bool {
	if d == nil {
		return false
	}
	return s.HomeAssistant.ExposeMode == "all" || (d.Adopted && d.HAEnabled)
}

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
	category       string // field | diagnostic | control
	cfg            map[string]any
}

var channelState = regexp.MustCompile(`^ch\d+_(state|digital_in|val|analog_in|temperature|humidity)$`)

// hiddenReason explains why an entity is not exposed; "" means it is.
func hiddenReason(d *model.Device, e haEntity, s config.Settings) string {
	ha := s.HomeAssistant
	if d.HAFields[e.key].Disabled {
		return "turned off for this node"
	}
	switch e.category {
	case "diagnostic":
		if !slices.Contains(ha.Diagnostics, e.key) {
			return "diagnostic turned off"
		}
	case "control":
		if !ha.Switches || !s.MQTTCommands.AcceptSet {
			return "switches turned off"
		}
	default:
		for _, pat := range ha.ExcludeFields {
			if ok, _ := path.Match(pat, e.key); ok {
				return "excluded by " + pat
			}
		}
	}
	return ""
}

// entities lists the entities a node exposes. The result depends only on the
// node, its field names and the settings, so it can also retract them.
func entities(d *model.Device, decoded map[string]any, s config.Settings) []haEntity {
	var out []haEntity
	for _, e := range allEntities(d, decoded, s) {
		if hiddenReason(d, e, s) == "" {
			out = append(out, e)
		}
	}
	return out
}

// allEntities lists every entity a node could expose.
func allEntities(d *model.Device, decoded map[string]any, s config.Settings) []haEntity {
	stateTopic := fmt.Sprintf("%s/%s/%s/state", s.MQTTTopicPrefix, d.AppID, d.ID)
	id := sanitize("lorahub_" + d.AppID + "_" + d.ID)
	var out []haEntity
	add := func(component, key, name, tpl string, extra map[string]any) {
		cfg := map[string]any{"name": name, "has_entity_name": true, "unique_id": sanitize(id + "_" + key),
			"state_topic": stateTopic, "value_template": tpl}
		category := "field"
		if component == "switch" {
			category = "control"
		} else if extra["entity_category"] == "diagnostic" {
			category = "diagnostic"
		}
		for k, v := range extra {
			cfg[k] = v
		}
		if ov := d.HAFields[sanitize(key)]; ov != (model.HAField{}) {
			if ov.Name != "" {
				cfg["name"] = ov.Name
			}
			if ov.DeviceClass != "" {
				cfg["device_class"] = ov.DeviceClass
			}
			if ov.Unit != "" {
				cfg["unit_of_measurement"] = ov.Unit
			}
		}
		if s.HomeAssistant.ExpireAfter > 0 && component != "switch" {
			cfg["expire_after"] = s.HomeAssistant.ExpireAfter
		}
		out = append(out, haEntity{component, sanitize(key), category, cfg})
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

func nodeMarker(d *model.Device) string { return "/" + sanitize("lorahub_"+d.AppID+"_"+d.ID) + "/" }

func nodeTopics(d *model.Device, s config.Settings) map[string]bool {
	out := map[string]bool{}
	if d == nil || d.LastDecoded == nil || !s.HomeAssistant.Enabled || !exposed(d, s) {
		return out
	}
	for _, e := range entities(d, normalize(d.LastDecoded), s) {
		out[entityTopic(s, d, e)] = true
	}
	return out
}

// syncNode retracts what a node no longer exposes and publishes the rest.
func (m *MQTT) syncNode(before *model.Device, bs config.Settings, after *model.Device, as config.Settings) {
	current := nodeTopics(after, as)
	for t := range nodeTopics(before, bs) {
		if !current[t] {
			m.publish(t, []byte{}, true)
		}
	}
	for _, d := range []*model.Device{before, after} {
		if d == nil {
			continue
		}
		m.mu.Lock()
		for t := range m.haSent {
			if strings.Contains(t, nodeMarker(d)) {
				delete(m.haSent, t)
			}
		}
		m.mu.Unlock()
	}
	if len(current) > 0 {
		m.publishEntities(after, normalize(after.LastDecoded), after.LastGateway, as)
	}
}

// NodeChanged applies an edit or deletion (after == nil) to Home Assistant.
func (m *MQTT) NodeChanged(before, after *model.Device) {
	s := m.settings()
	m.syncNode(before, s, after, s)
}

// gatewayField is a value a gateway can expose to Home Assistant.
type gatewayField struct{ key, name, class, unit string }

var gatewayFields = []gatewayField{
	{"rx", "Packets received", "", ""}, {"tx", "Packets sent", "", ""}, {"crc_err", "CRC errors", "", ""}, {"tx_err", "TX errors", "", ""},
	{"uptime_s", "Uptime", "duration", "s"}, {"temp_c", "Concentrator temperature", "temperature", "°C"},
}

func gatewayTopic(s config.Settings, gatewayID, key string) string {
	return fmt.Sprintf("%s/sensor/%s/%s/config", s.HomeAssistant.DiscoveryPrefix, sanitize("lorahub_gw_"+gatewayID), key)
}

func gatewayExposes(s config.Settings, key string) bool {
	ha := s.HomeAssistant
	return ha.Enabled && ha.Gateways && slices.Contains(ha.GatewayFields, key)
}

// gatewayDiscovery exposes a gateway's counters, as the parent of its nodes.
func (m *MQTT) gatewayDiscovery(gatewayID string, data any, s config.Settings) {
	status := normalize(data)
	if status == nil {
		return
	}
	id := sanitize("lorahub_gw_" + gatewayID)
	device := map[string]any{"identifiers": []string{id}, "name": "LoRa gateway " + gatewayID, "manufacturer": "LoRaHub"}
	for _, k := range gatewayFields {
		if _, ok := status[k.key]; !ok || !gatewayExposes(s, k.key) {
			continue
		}
		topic := gatewayTopic(s, gatewayID, k.key)
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
		if s.HomeAssistant.ExpireAfter > 0 {
			cfg["expire_after"] = s.HomeAssistant.ExpireAfter
		}
		b, _ := json.Marshal(cfg)
		m.publish(topic, b, true)
	}
}

// ReconcileHA applies a settings change to what Home Assistant already has.
func (m *MQTT) ReconcileHA(before, after config.Settings) {
	if m.Devices != nil {
		for _, d := range m.Devices() {
			m.syncNode(d, before, d, after)
		}
	}
	if m.Gateways != nil {
		for _, id := range m.Gateways() {
			for _, k := range gatewayFields {
				oldTopic := gatewayTopic(before, id, k.key)
				gone := gatewayExposes(before, k.key) && (!gatewayExposes(after, k.key) || oldTopic != gatewayTopic(after, id, k.key))
				if gone {
					m.publish(oldTopic, []byte{}, true)
				}
				m.mu.Lock()
				delete(m.haSent, oldTopic)
				delete(m.haSent, gatewayTopic(after, id, k.key))
				m.mu.Unlock()
			}
		}
	}
}

// RepublishHA sends every discovery message again.
func (m *MQTT) RepublishHA() {
	s := m.settings()
	m.ReconcileHA(s, s)
}

// RemoveHA deletes everything LoRaHub has announced to Home Assistant. Nodes
// and gateways announce themselves again with their next packet or status.
func (m *MQTT) RemoveHA() {
	s := m.settings()
	if m.Devices != nil {
		for _, d := range m.Devices() {
			m.syncNode(d, s, nil, s)
		}
	}
	if m.Gateways != nil {
		for _, id := range m.Gateways() {
			for _, k := range gatewayFields {
				m.publish(gatewayTopic(s, id, k.key), []byte{}, true)
				m.mu.Lock()
				delete(m.haSent, gatewayTopic(s, id, k.key))
				m.mu.Unlock()
			}
		}
	}
}

// ForgetGateway withdraws what was published under a gateway's former ID.
func (m *MQTT) ForgetGateway(id string) {
	s := m.settings()
	for _, k := range gatewayFields {
		m.publish(gatewayTopic(s, id, k.key), []byte{}, true)
	}
	m.publish(s.MQTTTopicPrefix+"/gateways/"+id+"/status", []byte{}, true)
}

func (m *MQTT) handleBirth(payload []byte) {
	if strings.TrimSpace(string(payload)) == "online" && m.settings().HomeAssistant.RepublishOnBirth {
		m.RepublishHA()
	}
}

// EntityView describes one entity on the exposure page.
type EntityView struct {
	Key         string `json:"key"`
	Component   string `json:"component"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Exposed     bool   `json:"exposed"`
	Reason      string `json:"reason,omitempty"`
	DeviceClass any    `json:"device_class,omitempty"`
	Unit        any    `json:"unit,omitempty"`
}

// EntityReport lists what every node and gateway exposes, and why not.
func (m *MQTT) EntityReport() map[string]any {
	s := m.settings()
	nodes := []map[string]any{}
	if m.Devices != nil {
		for _, d := range m.Devices() {
			views := []EntityView{}
			if d.LastDecoded != nil {
				for _, e := range allEntities(d, normalize(d.LastDecoded), s) {
					reason := hiddenReason(d, e, s)
					name, _ := e.cfg["name"].(string)
					views = append(views, EntityView{Key: e.key, Component: e.component, Name: name, Category: e.category, Exposed: reason == "" && exposed(d, s) && s.HomeAssistant.Enabled,
						Reason: reason, DeviceClass: e.cfg["device_class"], Unit: e.cfg["unit_of_measurement"]})
				}
			}
			nodes = append(nodes, map[string]any{"group": d.AppID, "node": d.ID, "name": d.Name, "adopted": d.Adopted, "ha_enabled": d.HAEnabled, "exposed": exposed(d, s),
				"has_data": d.LastDecoded != nil, "ha_fields": d.HAFields, "entities": views})
		}
	}
	return map[string]any{"enabled": s.HomeAssistant.Enabled, "nodes": nodes}
}
