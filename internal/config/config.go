// Package config holds the static (file) configuration and the runtime
// settings that are editable from the web console.
package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config is loaded from /etc/lorahub/lorahub.yml.
type Config struct {
	DataDir string `yaml:"data_dir"`
	HTTP    struct {
		Listen string `yaml:"listen"`
	} `yaml:"http"`
	SemtechUDP struct {
		Enabled bool   `yaml:"enabled"`
		Listen  string `yaml:"listen"`
	} `yaml:"semtech_udp"`
	Bobcat struct {
		Enabled      bool   `yaml:"enabled"`
		Listen       string `yaml:"listen"`
		DownlinkPort int    `yaml:"downlink_port"`
		// Optional EUI override for raw_lora_app instances that never
		// announce themselves (keyed by source IP).
		StaticEUIs map[string]string `yaml:"static_euis"`
	} `yaml:"bobcat_raw"`
	MQTTBroker struct {
		Enabled  bool   `yaml:"enabled"`
		Listen   string `yaml:"listen"`
		WSListen string `yaml:"ws_listen"`
		Username string `yaml:"username"`
		Password string `yaml:"password"`
	} `yaml:"mqtt_broker"`
	Concentrator struct {
		ConfigPath     string `yaml:"config_path"`
		RestartCommand string `yaml:"restart_command"`
		StatusAddr     string `yaml:"status_addr"`
		Service        string `yaml:"service"`
	} `yaml:"concentrator"`
}

// Default returns the default configuration.
func Default() *Config {
	c := &Config{DataDir: "/var/lib/lorahub"}
	c.HTTP.Listen = ":80"
	c.SemtechUDP.Enabled = true
	c.SemtechUDP.Listen = ":1700"
	c.Bobcat.Enabled = true
	c.Bobcat.Listen = ":5555"
	c.Bobcat.DownlinkPort = 5556
	c.MQTTBroker.Enabled = true
	c.MQTTBroker.Listen = ":1883"
	c.Concentrator.ConfigPath = "/etc/lora/config.json"
	c.Concentrator.RestartCommand = "systemctl restart raw-lora"
	c.Concentrator.StatusAddr = "127.0.0.1:5557"
	c.Concentrator.Service = "raw-lora"
	return c
}

// Load reads a YAML config file on top of the defaults.
func Load(path string) (*Config, error) {
	c := Default()
	if path == "" {
		return c, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, err
	}
	return c, nil
}

// PacketFormat describes the node packet header: NODE_ID | SERIAL | data.
type PacketFormat struct {
	NodeIDOffset int `json:"node_id_offset"`
	NodeIDLength int `json:"node_id_length"`
	SerialOffset int `json:"serial_offset"`
	SerialLength int `json:"serial_length"`
	DataOffset   int `json:"data_offset"`
}

// Settings are runtime settings stored in the database.
type Settings struct {
	PacketFormat     PacketFormat `json:"packet_format"`
	AutoCreateGroups bool         `json:"auto_create_groups"`
	LoRaWANEnabled   bool         `json:"lorawan_enabled"`

	DedupWindowMs        int    `json:"dedup_window_ms"`
	AutoRegisterGateways bool   `json:"auto_register_gateways"`
	DropCRCErrors        bool   `json:"drop_crc_errors"`
	DefaultBand          string `json:"default_frequency_plan"`
	NetID                string `json:"net_id"`
	DevAddrPrefix        string `json:"dev_addr_prefix"` // hex, e.g. 26000000
	DevAddrPrefixBits    int    `json:"dev_addr_prefix_bits"`
	RX1Delay             int    `json:"rx1_delay_s"`
	EventRetentionHours  int    `json:"event_retention_hours"`
	EventMaxRows         int    `json:"event_max_rows"`
	StoreGatewayRaw      bool   `json:"store_gateway_raw_events"`
	GatewayOfflineSec    int    `json:"gateway_offline_after_s"`
	LinkKey              string `json:"link_key"`       // shared with paired raw_lora_app gateways
	LinkAutoPair         bool   `json:"link_auto_pair"` // give the key to gateways that have none

	MQTTTopicPrefix string `json:"mqtt_topic_prefix"`
	ExternalMQTT    struct {
		Enabled  bool   `json:"enabled"`
		URL      string `json:"url"` // tcp://host:1883
		Username string `json:"username"`
		Password string `json:"password"`
		ClientID string `json:"client_id"`
	} `json:"external_mqtt"`
	MQTTPublish struct {
		Up              bool `json:"up"`
		IncludeGateways bool `json:"include_gateways"`
		IncludeRadio    bool `json:"include_radio"`
		IncludeRaw      bool `json:"include_raw"`
		State           bool `json:"state"`
		StateRetain     bool `json:"state_retain"`
		GatewayUp       bool `json:"gateway_up"`
		GatewayStatus   bool `json:"gateway_status"`
		Unmatched       bool `json:"unmatched"`
		DownEvents      bool `json:"down_events"`
		QoS             byte `json:"qos"`
	} `json:"mqtt_publish"`
	MQTTCommands struct {
		AcceptDown bool   `json:"accept_down"`
		AcceptSet  bool   `json:"accept_set"`
		OnHex      string `json:"on_hex"`
		OffHex     string `json:"off_hex"`
	} `json:"mqtt_commands"`
	HomeAssistant struct {
		Enabled          bool     `json:"enabled"`
		DiscoveryPrefix  string   `json:"discovery_prefix"`
		ExposeMode       string   `json:"expose_mode"` // adopted | all
		ExcludeFields    []string `json:"exclude_fields"`
		Diagnostics      []string `json:"diagnostics"`
		Switches         bool     `json:"switches"`
		ExpireAfter      int      `json:"expire_after_s"`
		Gateways         bool     `json:"gateways"`
		GatewayFields    []string `json:"gateway_fields"`
		RepublishOnBirth bool     `json:"republish_on_birth"`
		LastPayload      bool     `json:"last_payload"`
	} `json:"home_assistant"`

	FSKBridge FSKBridge `json:"fsk_bridge"`
}

// FSKBridge passes FSK packets between the gateways and another program over
// UDP, untouched: they are not node packets and skip all processing.
type FSKBridge struct {
	Enabled bool   `json:"enabled"`
	SendTo  string `json:"send_to"` // host:port that gets every FSK packet heard
	Listen  string `json:"listen"`  // UDP address whose datagrams are sent as FSK, e.g. ":1730"
	Format  string `json:"format"`  // json (data and metadata) or raw (the bytes only), both ways

	// How packets from Listen go on the air, unless a JSON datagram says otherwise.
	Gateway   string  `json:"gateway"`   // gateway ID; empty = the one that heard the last FSK packet best
	Frequency float64 `json:"frequency"` // MHz
	BitRate   int     `json:"bit_rate"`  // bit/s
	Deviation int     `json:"deviation"` // kHz
	Power     int     `json:"power"`     // dBm
	Preamble  int     `json:"preamble"`  // bytes
}

// DefaultSettings returns the default runtime settings.
func DefaultSettings() Settings {
	s := Settings{
		AutoCreateGroups:     true,
		DedupWindowMs:        250,
		AutoRegisterGateways: true,
		DropCRCErrors:        true,
		DefaultBand:          "EU868",
		NetID:                "000000",
		DevAddrPrefix:        "26000000",
		DevAddrPrefixBits:    7,
		RX1Delay:             1,
		EventRetentionHours:  24 * 7,
		EventMaxRows:         200000,
		StoreGatewayRaw:      true,
		GatewayOfflineSec:    120,
		LinkAutoPair:         true,
		MQTTTopicPrefix:      "lorahub",
		PacketFormat:         PacketFormat{NodeIDOffset: 0, NodeIDLength: 2, SerialOffset: 2, SerialLength: 4, DataOffset: 6},
	}
	p := &s.MQTTPublish
	p.Up, p.IncludeGateways, p.IncludeRadio, p.IncludeRaw, p.State, p.StateRetain = true, true, true, true, true, true
	p.GatewayUp, p.GatewayStatus, p.Unmatched, p.DownEvents = true, true, true, true
	s.MQTTCommands.AcceptDown, s.MQTTCommands.AcceptSet = true, true
	s.MQTTCommands.OnHex, s.MQTTCommands.OffHex = "0101", "0100"
	ha := &s.HomeAssistant
	ha.DiscoveryPrefix, ha.ExposeMode = "homeassistant", "adopted"
	ha.Diagnostics = []string{"rssi", "snr", "freq", "datr", "gateway_count", "last_seen", "seq"}
	ha.Switches, ha.Gateways, ha.RepublishOnBirth, ha.LastPayload = true, true, true, true
	ha.GatewayFields = []string{"rx", "tx", "crc_err", "tx_err", "uptime_s", "temp_c"}
	s.FSKBridge = FSKBridge{Listen: ":1730", Format: "json", Frequency: 868.3, BitRate: 50000, Deviation: 25, Power: 14, Preamble: 5}
	return s
}
