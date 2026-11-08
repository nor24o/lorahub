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

// Settings are runtime settings stored in the database.
type Settings struct {
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

	MQTTTopicPrefix string `json:"mqtt_topic_prefix"`
	ExternalMQTT    struct {
		Enabled  bool   `json:"enabled"`
		URL      string `json:"url"` // tcp://host:1883
		Username string `json:"username"`
		Password string `json:"password"`
		ClientID string `json:"client_id"`
	} `json:"external_mqtt"`
	HomeAssistant struct {
		Enabled         bool   `json:"enabled"`
		DiscoveryPrefix string `json:"discovery_prefix"`
	} `json:"home_assistant"`
}

// DefaultSettings returns the default runtime settings.
func DefaultSettings() Settings {
	s := Settings{
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
		MQTTTopicPrefix:      "v3",
	}
	s.HomeAssistant.DiscoveryPrefix = "homeassistant"
	return s
}
