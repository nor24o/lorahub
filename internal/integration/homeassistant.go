package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nor24o/lorahub/internal/config"
	"github.com/nor24o/lorahub/internal/model"
)

// handleSet accepts simple commands on <prefix>/<group>/<node>/set:
// ON, OFF, 1, 0, DESCRIBE (asks the node to describe its commands) or a hex payload.
func (m *MQTT) handleSet(appID, devID string, payload []byte) {
	cmd := strings.TrimSpace(string(payload))
	cmds := m.settings().MQTTCommands
	switch strings.ToUpper(cmd) {
	case "ON", "1":
		cmd = cmds.OnHex
	case "OFF", "0":
		cmd = cmds.OffHex
	case "DESCRIBE":
		cmd = fmt.Sprintf("%02X", model.DescribeRequest)
	}
	b, err := hexDecode(cmd)
	if err != nil || len(b) == 0 {
		m.log.Warn("mqtt set: expected ON, OFF or hex", "group", appID, "node", devID)
		return
	}
	if err := m.downlink(appID, devID, 1, b, nil, false, false); err != nil {
		m.log.Warn("mqtt set", "group", appID, "node", devID, "err", err)
	}
	m.flashCommandBox(appID, devID, cmd)
}

// commandBoxHold is how long a sent command stays visible in the Send downlink box.
const commandBoxHold = 1500 * time.Millisecond

func commandBoxTopic(s config.Settings, appID, devID string) string {
	return fmt.Sprintf("%s/%s/%s/command_box", s.MQTTTopicPrefix, appID, devID)
}

func commandBoxValue(v string) []byte {
	b, _ := json.Marshal(map[string]string{"value": v})
	return b
}

// flashCommandBox shows a command in the Send downlink box, then empties it.
// Home Assistant only sends a value that differs from the entity's state, so
// the box has to return to empty before the same command can be sent again.
func (m *MQTT) flashCommandBox(appID, devID, value string) {
	s := m.settings()
	if s.HomeAssistant.Enabled {
		m.flash(commandBoxTopic(s, appID, devID), value)
	}
}

func (m *MQTT) flash(topic, value string) {
	m.publish(topic, commandBoxValue(value), true)
	time.AfterFunc(commandBoxHold, func() { m.publish(topic, commandBoxValue(""), true) })
}

func controlTopic(s config.Settings, appID, devID, key string) string {
	return fmt.Sprintf("%s/%s/%s/cmd/%s", s.MQTTTopicPrefix, appID, devID, key)
}

func findControl(d *model.Device, key string) *model.HACommand {
	cmds := d.Commands() // set in LoRaHub, and announced by the node
	for i := range cmds {
		if c := &cmds[i]; c.Dynamic() && c.Key() == key {
			return c
		}
	}
	return nil
}

// controlValue is what a dynamic command shows: its last value, or the minimum
// so a number never starts out unknown.
// hasOwnOutputSwitch reports whether one of the node's switches, announced by
// the node or set in LoRaHub, sends what the generic Output switch would (ON
// 0101, OFF 0100). Output is then left out: two switches would do the same.
func hasOwnOutputSwitch(cmds []model.HACommand) bool {
	for i := range cmds {
		c := &cmds[i]
		if c.Kind != "switch" {
			continue
		}
		on, err1 := c.Render("ON")
		off, err2 := c.Render("OFF")
		if err1 == nil && err2 == nil && bytes.Equal(on, []byte{1, 1}) && bytes.Equal(off, []byte{1, 0}) {
			return true
		}
	}
	return false
}

func controlValue(d *model.Device, c *model.HACommand) string {
	switch c.Kind {
	case "switch":
		if v := d.HAValues[c.Key()]; v == "ON" || v == "OFF" {
			return v
		}
		return "OFF"
	case "choice":
		if o := c.Option(d.HAValues[c.Key()]); o != nil {
			return o.Name
		}
		if len(c.Options) > 0 {
			return c.Options[0].Name
		}
		return ""
	case "number":
	default:
		return ""
	}
	last := d.HAValues[c.Key()]
	if v, err := strconv.ParseFloat(last, 64); err == nil && v >= c.Min && v <= c.Max {
		return last
	}
	return strconv.FormatFloat(c.Min, 'f', -1, 64)
}

// handleControl sends a value given to a dynamic command on
// <prefix>/<group>/<node>/cmd/<key>. Only class C nodes have these: they
// listen all the time, so the value takes effect when it is set.
func (m *MQTT) handleControl(appID, devID, key string, payload []byte) {
	if m.Device == nil {
		return
	}
	d, err := m.Device(appID, devID)
	if err != nil {
		m.log.Warn("mqtt control", "group", appID, "node", devID, "err", err)
		return
	}
	c := findControl(d, key)
	if c == nil || d.Class != "C" {
		m.log.Warn("mqtt control: no such control on a class C node", "group", appID, "node", devID, "control", key)
		return
	}
	topic := controlTopic(m.settings(), appID, devID, key) + "/state"
	value := strings.TrimSpace(string(payload))
	b, err := c.Render(value)
	if err == nil {
		err = m.downlink(appID, devID, 1, b, nil, false, false)
	}
	if err != nil {
		m.log.Warn("mqtt control", "group", appID, "node", devID, "control", key, "err", err)
		m.publish(topic, commandBoxValue(controlValue(d, c)), true) // back to what the node last got
		return
	}
	if c.Kind == "text" {
		m.flash(topic, value)
		return
	}
	m.publish(topic, commandBoxValue(value), true)
	if m.SaveControlValue != nil {
		m.SaveControlValue(appID, devID, key, value)
	}
}

type upMessage struct {
	Decoded      map[string]any   `json:"decoded"`
	Gateways     []map[string]any `json:"gateways"`
	Radio        map[string]any   `json:"radio"`
	PayloadHex   string           `json:"payload_hex"`
	Size         int              `json:"size"`
	GatewayCount int              `json:"gateway_count"`
	ReceivedAt   string           `json:"received_at"`
	Simulated    bool             `json:"simulated"`
}

// flatState builds a node's flat state from an as.up message.
func flatState(msg *upMessage) ([]byte, string) {
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
	return b, gateway
}

func stateTopic(s config.Settings, appID, devID string) string {
	return fmt.Sprintf("%s/%s/%s/state", s.MQTTTopicPrefix, appID, devID)
}

// nodeState publishes the flat retained state of a node and keeps its Home
// Assistant entities up to date. raw is the JSON of the as.up message.
func (m *MQTT) nodeState(e *model.Event, s config.Settings, raw []byte) {
	var msg upMessage
	if err := json.Unmarshal(raw, &msg); err != nil || msg.Simulated { // test packets never reach Home Assistant
		return
	}
	b, gateway := flatState(&msg)
	// Home Assistant's node entities read this topic.
	if s.MQTTPublish.State || s.HomeAssistant.Enabled {
		m.publish(stateTopic(s, e.AppID, e.DevID), b, s.MQTTPublish.StateRetain || s.HomeAssistant.Enabled)
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
		if e.key == "hex" {
			if !ha.LastPayload {
				return "last payload turned off"
			}
			return ""
		}
		if !slices.Contains(ha.Diagnostics, e.key) {
			return "diagnostic turned off"
		}
	case "control", "dynamic":
		if !ha.Switches || !s.MQTTCommands.AcceptSet {
			return "commands turned off"
		}
		if e.category == "dynamic" && d.Class != "C" {
			return "class C nodes only"
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
	cmds := d.Commands()
	// A command whose name matches a decoded field takes its state from that
	// field, as the node reports it, and the field is not shown twice.
	stateField := map[string]string{}
	for i := range cmds {
		if c := &cmds[i]; c.Kind == "switch" || c.Kind == "number" || c.Kind == "choice" {
			if k := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(c.Name), " ", "_")); flat[k] != nil {
				stateField[c.Key()] = k
			}
		}
	}
	claimed := map[string]bool{}
	for _, k := range stateField {
		claimed[k] = true
	}
	isSwitch := (d.Class == "C" || d.HASwitch || strings.Contains(strings.ToLower(d.Model), "valve") ||
		strings.Contains(strings.ToLower(d.Model), "relay") || strings.Contains(strings.ToLower(d.Model), "actuator")) &&
		!hasOwnOutputSwitch(cmds)
	_, hasState := flat["state"]
	cmd := fmt.Sprintf("%s/%s/%s/set", s.MQTTTopicPrefix, d.AppID, d.ID)
	command := func(component, key, name string, extra map[string]any) {
		cfg := map[string]any{"name": name, "has_entity_name": true, "unique_id": sanitize(id + "_" + key), "command_topic": cmd}
		for k, v := range extra {
			cfg[k] = v
		}
		if ov := d.HAFields[sanitize(key)]; ov.Name != "" {
			cfg["name"] = ov.Name
		}
		out = append(out, haEntity{component, sanitize(key), "control", cfg})
	}
	for _, k := range keys {
		switch {
		case k == "node_class", k == "capability", k == "device_type", channelState.MatchString(k), claimed[k]:
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
	switch {
	case isSwitch && hasState:
		add("switch", "switch", "Output", "{{ value_json['state'] | default('OFF') }}", map[string]any{"command_topic": cmd,
			"payload_on": "ON", "payload_off": "OFF", "state_on": "ON", "state_off": "OFF", "optimistic": false})
	case isSwitch:
		// The node reports no state: reading the state topic would turn the switch off at every uplink.
		command("switch", "switch", "Output", map[string]any{"payload_on": "ON", "payload_off": "OFF", "optimistic": true})
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
	// A short preview fits Home Assistant's value field; the entity's details carry the whole payload.
	add("sensor", "hex", "Last payload", "{% set h = value_json['hex'] | default('') %}{{ h[:16] ~ ('…' if h | length > 16 else '') }}", diag(map[string]any{
		"icon": "mdi:hexadecimal", "json_attributes_topic": stateTopic,
		"json_attributes_template": "{{ {'payload': value_json['hex'] | default(''), 'bytes': value_json['size'] | default(0)} | tojson }}"}))
	command("text", "downlink", "Send downlink", map[string]any{"mode": "text", "pattern": "^([0-9A-Fa-f]{2})*$", "max": 250, "icon": "mdi:send",
		"state_topic": commandBoxTopic(s, d.AppID, d.ID), "value_template": "{{ value_json['value'] }}"})
	for i := range cmds {
		c := &cmds[i]
		if !c.Dynamic() {
			command("button", c.Key(), c.Name, map[string]any{"payload_press": c.PayloadHex, "icon": "mdi:gesture-tap-button"})
			continue
		}
		topic := controlTopic(s, d.AppID, d.ID, c.Key())
		extra := map[string]any{"command_topic": topic, "state_topic": topic + "/state", "value_template": "{{ value_json['value'] }}"}
		field := stateField[c.Key()]
		if field != "" {
			extra["state_topic"] = stateTopic
		}
		switch c.Kind {
		case "number":
			extra["min"], extra["max"], extra["step"], extra["icon"] = c.Min, c.Max, c.Step, "mdi:tune-variant"
			if c.Unit != "" {
				extra["unit_of_measurement"] = c.Unit
			}
			if field != "" {
				extra["value_template"] = val(field)
			}
		case "switch":
			extra["payload_on"], extra["payload_off"], extra["state_on"], extra["state_off"] = "ON", "OFF", "ON", "OFF"
			extra["icon"] = "mdi:toggle-switch-outline"
			if field != "" {
				extra["value_template"] = fmt.Sprintf("{{ 'ON' if value_json['%s'] in [1, true, 'ON', 'on', '1'] else 'OFF' }}", field)
			}
		case "choice":
			names := make([]string, len(c.Options))
			pairs := make([]string, len(c.Options))
			for j, o := range c.Options {
				names[j] = o.Name
				pairs[j] = fmt.Sprintf("%s: %q", strconv.FormatFloat(o.Value, 'f', -1, 64), o.Name)
			}
			extra["options"], extra["icon"] = names, "mdi:format-list-bulleted"
			if field != "" {
				extra["value_template"] = fmt.Sprintf("{%% set m = {%s} %%}{{ m.get(value_json['%s'] | int(-1), none) }}", strings.Join(pairs, ", "), field)
			}
		default: // text
			extra["mode"], extra["max"], extra["icon"] = "text", 200, "mdi:form-textbox"
			if c.Encoding == "hex" {
				extra["pattern"] = "^([0-9A-Fa-f]{2})*$"
			}
		}
		command(c.Component(), c.Key(), c.Name, extra)
		out[len(out)-1].category = "dynamic"
	}
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
		// Values are retained and sent first, so controls never start out unknown.
		if e.key == "downlink" {
			m.publish(commandBoxTopic(s, d.AppID, d.ID), commandBoxValue(""), true)
		} else if c := findControl(d, e.key); e.category == "dynamic" && c != nil {
			m.publish(controlTopic(s, d.AppID, d.ID, c.Key())+"/state", commandBoxValue(controlValue(d, c)), true)
		}
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
	if before != nil && len(current) == 0 {
		m.publish(commandBoxTopic(bs, before.AppID, before.ID), []byte{}, true)
	}
	if before != nil {
		cmds := before.Commands()
		for i := range cmds {
			c := &cmds[i]
			if c.Dynamic() && !current[entityTopic(as, before, haEntity{component: c.Component(), key: c.Key()})] {
				m.publish(controlTopic(bs, before.AppID, before.ID, c.Key())+"/state", []byte{}, true)
			}
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
		// Values first and retained, so entities never start out unknown. Once
		// more shortly after, for a Home Assistant still subscribing.
		m.publishLastState(after, as)
		m.publishEntities(after, normalize(after.LastDecoded), after.LastGateway, as)
		time.AfterFunc(3*time.Second, func() { m.publishLastState(after, as) })
	}
}

// publishLastState sends a node's most recent values as retained state.
func (m *MQTT) publishLastState(d *model.Device, s config.Settings) {
	var b []byte
	if m.LastMessage != nil {
		var msg upMessage
		if raw := m.LastMessage(d.AppID, d.ID); raw != nil && json.Unmarshal(raw, &msg) == nil {
			b, _ = flatState(&msg)
		}
	}
	if b == nil {
		b = deviceState(d)
	}
	m.publish(stateTopic(s, d.AppID, d.ID), b, true)
}

// deviceState is the state known from the node record alone, used when its
// last message has aged out of the history.
func deviceState(d *model.Device) []byte {
	state := map[string]any{}
	for k, v := range flatten(normalize(d.LastDecoded)) {
		state[k] = v
	}
	state["rssi"], state["snr"], state["gateway"], state["datr"] = d.LastRSSI, d.LastSNR, d.LastGateway, d.LastDataRate
	if d.LastFreq != 0 {
		state["freq"] = float64(d.LastFreq) / 1e6
	}
	if d.LastSeen != nil {
		state["last_seen"] = d.LastSeen.Format(time.RFC3339)
	}
	b, _ := json.Marshal(state)
	return b
}

// NodeChanged applies an edit or deletion (after == nil) to Home Assistant.
func (m *MQTT) NodeChanged(before, after *model.Device) {
	s := m.settings()
	m.syncMu.Lock()
	m.syncNode(before, s, after, s)
	m.syncMu.Unlock()
	m.SweepHA(false)
}

// ourDiscovery splits a discovery topic that LoRaHub published into
// component, object and (for current topics) entity key.
func ourDiscovery(prefix, topic string) ([]string, bool) {
	rest, ok := strings.CutPrefix(topic, prefix+"/")
	if !ok {
		return nil, false
	}
	parts := strings.Split(rest, "/")
	if (len(parts) != 3 && len(parts) != 4) || parts[len(parts)-1] != "config" || !strings.HasPrefix(parts[1], "lorahub_") {
		return nil, false
	}
	return parts[:len(parts)-1], true
}

func (m *MQTT) noteRetained(topic string, payload []byte) {
	if _, ours := ourDiscovery(m.settings().HomeAssistant.DiscoveryPrefix, topic); !ours {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.extRetained == nil {
		m.extRetained = map[string]bool{}
	}
	if len(payload) == 0 {
		delete(m.extRetained, topic)
	} else {
		m.extRetained[topic] = true
	}
}

var diagnosticKeys = map[string]bool{"rssi": true, "snr": true, "freq": true, "datr": true, "gateway_count": true, "last_seen": true, "hex": true, "seq": true}

// SweepHA withdraws discovery messages LoRaHub no longer stands behind: nodes
// and groups that were deleted or are no longer exposed, removed commands,
// and anything turned off. Unless strict, a field sensor of an exposed node
// stays even when the last packet lacked it, because nodes send different
// fields in different packets. It returns how many entities it withdrew.
func (m *MQTT) SweepHA(strict bool) int {
	return len(m.sweep(strict))
}

func (m *MQTT) sweep(strict bool) []string {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()
	s := m.settings()
	prefix := s.HomeAssistant.DiscoveryPrefix
	expected := map[string]bool{}
	nodes := map[string]*model.Device{}
	if m.Devices != nil {
		for _, d := range m.Devices() {
			for t := range nodeTopics(d, s) {
				expected[t] = true
			}
			if s.HomeAssistant.Enabled && exposed(d, s) {
				nodes[sanitize("lorahub_"+d.AppID+"_"+d.ID)] = d
			}
		}
	}
	if m.Gateways != nil {
		for _, id := range m.Gateways() {
			for _, k := range gatewayFields {
				if gatewayExposes(s, k.key) {
					expected[gatewayTopic(s, id, k.key)] = true
				}
			}
		}
	}
	candidates := map[string]bool{}
	if m.broker != nil {
		for _, pk := range m.broker.Topics.Messages(prefix + "/#") {
			if len(pk.Payload) > 0 {
				candidates[pk.TopicName] = true
			}
		}
	}
	m.mu.Lock()
	for t := range m.extRetained {
		candidates[t] = true
	}
	m.mu.Unlock()
	var removed []string
	for t := range candidates {
		parts, ours := ourDiscovery(prefix, t)
		if !ours || expected[t] {
			continue
		}
		if !strict && len(parts) == 3 && (parts[0] == "sensor" || parts[0] == "binary_sensor") && !diagnosticKeys[parts[2]] {
			if d := nodes[parts[1]]; d != nil && hiddenReason(d, haEntity{component: parts[0], key: parts[2], category: "field"}, s) == "" {
				continue
			}
		}
		m.publish(t, []byte{}, true)
		m.mu.Lock()
		delete(m.extRetained, t)
		delete(m.haSent, t)
		m.mu.Unlock()
		removed = append(removed, t)
	}
	if len(removed) > 0 {
		m.log.Info("withdrew stale Home Assistant entities", "count", len(removed))
	}
	return removed
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
	defer m.SweepHA(false)
	if m.Devices != nil {
		m.syncMu.Lock()
		for _, d := range m.Devices() {
			m.syncNode(d, before, d, after)
		}
		m.syncMu.Unlock()
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
			node := map[string]any{"group": d.AppID, "node": d.ID, "name": d.Name, "adopted": d.Adopted, "ha_enabled": d.HAEnabled, "exposed": exposed(d, s),
				"has_data": d.LastDecoded != nil, "ha_fields": d.HAFields, "ha_commands": d.HACommands, "described": d.Described, "ha_switch": d.HASwitch, "class": d.Class, "class_from_firmware": d.Profile != nil, "serial": d.Serial, "entities": views}
			if m.Group != nil {
				if app, err := m.Group(d.AppID); err == nil {
					node["node_id"], node["downlink_header"], node["downlink_control"] = app.NodeID, app.NodeHeaderOn(d), app.NodeControlOn(d)
				}
			}
			nodes = append(nodes, node)
		}
	}
	return map[string]any{"enabled": s.HomeAssistant.Enabled, "nodes": nodes}
}
