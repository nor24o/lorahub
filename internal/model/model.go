// Package model holds the shared data types of LoRaHub.
package model

import (
	"encoding/json"
	"time"
)

// Location is a WGS84 position.
type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Altitude  float64 `json:"altitude"`
	Source    string  `json:"source,omitempty"`
}

// Gateway is a registered gateway (the local concentrator or a satellite).
type Gateway struct {
	ID          string            `json:"gateway_id"`
	EUI         string            `json:"eui"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Kind        string            `json:"kind"` // bobcat | semtech_udp | http
	Band        string            `json:"frequency_plan"`
	Location    *Location         `json:"location,omitempty"`
	APIKey      string            `json:"api_key,omitempty"`
	Downlink    bool              `json:"downlink_enabled"`
	TxPower     int               `json:"tx_power_override,omitempty"`
	Disabled    bool              `json:"disabled"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	Forwarders  []Forwarder       `json:"forwarders,omitempty"`
	// Control and status ports of raw_lora_app; 0 means not set.
	ControlPort int `json:"control_port,omitempty"`
	StatusPort  int `json:"status_port,omitempty"`
	// Reception
	RSSIOffset float64  `json:"rssi_offset,omitempty"` // added to reported RSSI
	MinRSSI    float64  `json:"min_rssi,omitempty"`    // 0 = accept everything
	MinSNR     *float64 `json:"min_snr,omitempty"`
	// Transmission
	AntennaGain  float64 `json:"antenna_gain_dbi,omitempty"`
	DutyCyclePct float64 `json:"duty_cycle_pct,omitempty"` // 0 = no limit
	// OfflineAfter overrides the global offline timeout, in seconds.
	OfflineAfter int       `json:"offline_after_s,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`

	// Runtime state
	LastSeen   *time.Time      `json:"last_seen,omitempty"`
	LastAddr   string          `json:"last_addr,omitempty"`
	RxCount    uint64          `json:"rx_count"`
	TxCount    uint64          `json:"tx_count"`
	LastStatus json.RawMessage `json:"last_status,omitempty"`
}

// Forwarder forwards a gateway's raw traffic upstream (e.g. to TTN or
// another LoRaHub) using the Semtech UDP protocol.
type Forwarder struct {
	Name     string `json:"name"`
	Address  string `json:"address"` // host:port
	Enabled  bool   `json:"enabled"`
	Downlink bool   `json:"downlink"` // accept PULL_RESP from upstream
}

// Formatter configures payload decoding.
type Formatter struct {
	Type       string         `json:"type"` // none | javascript | cayenne_lpp | fields | inherit | a decoding method
	Code       string         `json:"code,omitempty"`
	Fields     []Field        `json:"fields,omitempty"`
	DataOffset int            `json:"data_offset,omitempty"` // for cayenne on raw devices
	Options    *DecodeOptions `json:"options,omitempty"`
}

// Field is a byte-range field for the "fields" formatter.
type Field struct {
	Name   string  `json:"name"`
	Offset int     `json:"offset"` // negative counts from the end
	Length int     `json:"length"` // 0 = from the type, -1 = to the end
	Type   string  `json:"type"`
	Scale  float64 `json:"scale"`
	Add    float64 `json:"add"`
	Unit   string  `json:"unit"`
	Bit    int     `json:"bit,omitempty"`

	BitLen   int               `json:"bit_length,omitempty"`
	Decimals *int              `json:"decimals,omitempty"`
	Min      *float64          `json:"min,omitempty"`
	Max      *float64          `json:"max,omitempty"`
	Invalid  string            `json:"invalid,omitempty"` // raw hex meaning "no reading"
	Map      map[string]string `json:"map,omitempty"`     // raw value -> label
	Expr     string            `json:"expr,omitempty"`    // JavaScript: x, i, bytes, v
	When     string            `json:"when,omitempty"`    // JavaScript: decode only if true
	Repeat   int               `json:"repeat,omitempty"`
	Stride   int               `json:"stride,omitempty"` // bytes between repeats
	Invert   bool              `json:"invert,omitempty"`
	Algo     string            `json:"algo,omitempty"` // checksum algorithm
	From     int               `json:"from,omitempty"` // checksum start byte
	Required bool              `json:"required,omitempty"`
	Tag      string            `json:"tag,omitempty"` // custom TLV: tag as hex
}

// DecodeOptions configures the decoding methods and preprocessing.
type DecodeOptions struct {
	Pre          string   `json:"pre,omitempty"` // hex_text | base64_text | xor | reverse
	PreKey       string   `json:"pre_key,omitempty"`
	Separator    string   `json:"separator,omitempty"`
	KVSeparator  string   `json:"kv_separator,omitempty"`
	Columns      []string `json:"columns,omitempty"`
	Pattern      string   `json:"pattern,omitempty"`
	PatternOn    string   `json:"pattern_on,omitempty"` // text | hex
	TagSize      int      `json:"tag_size,omitempty"`
	LenSize      int      `json:"len_size,omitempty"` // 0 = length follows from the tag's type
	LengthFirst  bool     `json:"length_first,omitempty"`
	LittleEndian bool     `json:"little_endian,omitempty"`
	Tags         []Field  `json:"tags,omitempty"`
}

// Application groups end devices and integrations.
type Application struct {
	ID          string            `json:"application_id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Uplink      Formatter         `json:"uplink_formatter"`
	Downlink    Formatter         `json:"downlink_formatter"`
	Webhooks    []Webhook         `json:"webhooks"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	// Node group: packets whose NODE_ID header equals NodeID belong here.
	NodeID          string       `json:"node_id"`
	AutoRegister    bool         `json:"auto_register_nodes"`
	DownlinkHeader  bool         `json:"downlink_header"` // prepend NODE_ID+SERIAL to downlinks
	RawDownlink     *RawDownlink `json:"downlink_radio,omitempty"`
	NodeProfile     bool         `json:"node_profile,omitempty"`     // nodes send a profile after SERIAL_ID
	DownlinkControl bool         `json:"downlink_control,omitempty"` // downlinks carry a control byte after SERIAL_ID
	CreatedAt       time.Time    `json:"created_at"`
	UpdatedAt       time.Time    `json:"updated_at"`
}

// Webhook posts events to an HTTP endpoint.
type Webhook struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Events  []string          `json:"events"` // up join downlink_sent unmatched
	Enabled bool              `json:"enabled"`
	Format  string            `json:"format"` // full | decoded
}

// RawMatch identifies non-LoRaWAN ("raw") devices by payload content.
type RawMatch struct {
	Type        string `json:"type"`   // prefix | bytes | regex_hex
	Offset      int    `json:"offset"` // for bytes
	Value       string `json:"value"`  // hex (prefix/bytes) or regex on hex
	Length      int    `json:"length,omitempty"`
	MinLen      int    `json:"min_length,omitempty"`
	StripPrefix bool   `json:"strip_prefix,omitempty"`
}

// RawDownlink describes how downlinks are sent to raw devices.
type RawDownlink struct {
	Mode       string  `json:"mode"` // immediate | after_uplink
	DelayMs    int     `json:"delay_ms"`
	Frequency  float64 `json:"frequency"` // MHz, 0 = same as uplink
	DataRate   string  `json:"data_rate"` // empty = same as uplink
	CodingRate string  `json:"coding_rate"`
	Power      int     `json:"power"`
	InvertPol  bool    `json:"invert_polarity"`
	Preamble   int     `json:"preamble,omitempty"` // symbols, 0 = the gateway's default
	NoCRC      bool    `json:"no_crc,omitempty"`
	NoHeader   bool    `json:"implicit_header,omitempty"`
	Gateway    string  `json:"gateway,omitempty"`      // gateway ID, empty = the one that heard the node best
	Header     string  `json:"node_header,omitempty"`  // on | off, empty = as the group says
	Control    string  `json:"node_control,omitempty"` // on | off, empty = as the group says
}

// Session is the LoRaWAN session state.
type Session struct {
	DevAddr   string    `json:"dev_addr"`
	NwkSKey   string    `json:"nwk_s_key"`
	AppSKey   string    `json:"app_s_key"`
	FCntUp    uint32    `json:"f_cnt_up"`
	FCntDown  uint32    `json:"f_cnt_down"`
	HasUplink bool      `json:"has_uplink"`
	StartedAt time.Time `json:"started_at"`
}

// QueuedDownlink is a pending application downlink.
type QueuedDownlink struct {
	ID        string    `json:"id"`
	FPort     int       `json:"f_port"`
	Payload   []byte    `json:"frm_payload"`
	Confirmed bool      `json:"confirmed"`
	CreatedAt time.Time `json:"created_at"`
	Decoded   any       `json:"decoded_payload,omitempty"`
	// ExpiresAt is set on broadcasts waiting for a class A receive window.
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Device is an end device.
type Device struct {
	ID            string             `json:"device_id"`
	AppID         string             `json:"application_id"`
	Name          string             `json:"name"`
	Description   string             `json:"description"`
	Mode          string             `json:"mode"`             // node | otaa | abp | raw
	Serial        string             `json:"serial,omitempty"` // node mode: SERIAL_ID hex
	Adopted       bool               `json:"adopted"`
	Model         string             `json:"model,omitempty"`
	Area          string             `json:"area,omitempty"`
	HAEnabled     bool               `json:"ha_enabled"`
	HAFields      map[string]HAField `json:"ha_fields,omitempty"`
	HACommands    []HACommand        `json:"ha_commands,omitempty"`
	HAValues      map[string]string  `json:"ha_values,omitempty"`    // last value of each dynamic command, by key
	HASwitch      bool               `json:"ha_switch,omitempty"`    // node has an on/off output
	Profile       *NodeProfile       `json:"profile,omitempty"`      // what the firmware last declared
	Described     *NodeDescription   `json:"described,omitempty"`    // the commands the node announced
	DownSeq       uint8              `json:"downlink_seq,omitempty"` // next sequence number in the control byte
	Band          string             `json:"frequency_plan"`
	Class         string             `json:"class"` // A | C
	DevEUI        string             `json:"dev_eui,omitempty"`
	JoinEUI       string             `json:"join_eui,omitempty"`
	AppKey        string             `json:"app_key,omitempty"`
	Session       *Session           `json:"session,omitempty"`
	SkipFCntCheck bool               `json:"skip_fcnt_check"`
	UsedNonces    []uint16           `json:"used_dev_nonces,omitempty"`
	RawMatch      *RawMatch          `json:"raw_match,omitempty"`
	RawFPort      int                `json:"raw_f_port"`
	RawDownlink   *RawDownlink       `json:"raw_downlink,omitempty"`
	Uplink        Formatter          `json:"uplink_formatter"`
	Downlink      Formatter          `json:"downlink_formatter"`
	Location      *Location          `json:"location,omitempty"`
	Attributes    map[string]string  `json:"attributes,omitempty"`
	Queue         []QueuedDownlink   `json:"downlink_queue"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`

	// Runtime
	LastSeen     *time.Time `json:"last_seen,omitempty"`
	UplinkCount  uint64     `json:"uplink_count"`
	LastRSSI     float64    `json:"last_rssi"`
	LastSNR      float64    `json:"last_snr"`
	LastGateway  string     `json:"last_gateway,omitempty"`
	LastFreq     uint64     `json:"last_frequency,omitempty"`
	LastDataRate string     `json:"last_data_rate,omitempty"`
	LastDecoded  any        `json:"last_decoded,omitempty"`
	LastScheme   string     `json:"last_scheme,omitempty"`
}

// HAField overrides how one entity of a node appears in Home Assistant.
type HAField struct {
	Disabled    bool   `json:"disabled,omitempty"`
	Name        string `json:"name,omitempty"`
	DeviceClass string `json:"device_class,omitempty"`
	Unit        string `json:"unit,omitempty"`
}

// Rule sends a downlink to one node when a decoded value of another crosses
// a threshold.
type Rule struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Enabled     bool    `json:"enabled"`
	SourceApp   string  `json:"source_group"`
	SourceDev   string  `json:"source_node"`
	Field       string  `json:"field"`
	Operator    string  `json:"operator"` // > < >= <= == != always
	Threshold   float64 `json:"threshold"`
	TargetApp   string  `json:"target_group"`
	TargetDev   string  `json:"target_node"`
	PayloadHex  string  `json:"payload_hex"`
	CooldownSec int     `json:"cooldown_s"`
	PulseSec    int     `json:"pulse_s"`
	PulseOffHex string  `json:"pulse_off_hex"`

	LastTriggered *time.Time `json:"last_triggered,omitempty"`
	LastStatus    string     `json:"last_status,omitempty"`
}

// RxMetadata is the per-gateway reception metadata of an uplink.
type RxMetadata struct {
	GatewayIDs  GatewayIDs     `json:"gateway_ids"`
	Source      string         `json:"source"`
	Time        *time.Time     `json:"time,omitempty"`
	Timestamp   uint32         `json:"timestamp"`
	RSSI        float64        `json:"rssi"`
	ChannelRSSI float64        `json:"channel_rssi"`
	SignalRSSI  *float64       `json:"signal_rssi,omitempty"`
	SNR         float64        `json:"snr"`
	FreqOffset  int            `json:"frequency_offset"`
	Channel     int            `json:"channel_index"`
	RFChain     int            `json:"rf_chain"`
	CRCStatus   string         `json:"crc_status"`
	Location    *Location      `json:"location,omitempty"`
	ReceivedAt  time.Time      `json:"received_at"`
	Extra       map[string]any `json:"extra,omitempty"`
}

// GatewayIDs identifies a gateway.
type GatewayIDs struct {
	GatewayID string `json:"gateway_id"`
	EUI       string `json:"eui"`
}

// LoRaSettings are the modulation settings.
type LoRaSettings struct {
	Modulation string `json:"modulation"`
	SF         int    `json:"spreading_factor,omitempty"`
	BW         int    `json:"bandwidth,omitempty"`
	BitRate    int    `json:"bit_rate,omitempty"`
	CodingRate string `json:"coding_rate,omitempty"`
	Frequency  uint64 `json:"frequency"`
	DataRate   string `json:"data_rate"`
}

// RxPacket is a single reception by one gateway.
type RxPacket struct {
	Payload    []byte
	Settings   LoRaSettings
	Meta       RxMetadata
	GatewayEUI string
}

// TxPacket is a downlink to send through a gateway.
type TxPacket struct {
	Payload    []byte `json:"-"`
	Frequency  uint64 `json:"frequency"`
	DataRate   string `json:"data_rate"`
	CodingRate string `json:"coding_rate"`
	Power      int    `json:"power"`
	InvertPol  bool   `json:"invert_polarity"`
	Immediate  bool   `json:"immediate"`
	Timestamp  uint32 `json:"timestamp"`
	RFChain    int    `json:"rf_chain"`
	Preamble   int    `json:"preamble,omitempty"`
	NoCRC      bool   `json:"no_crc"`
	NoHeader   bool   `json:"implicit_header,omitempty"`
	Modulation string `json:"modulation,omitempty"` // empty or LORA; FSK: DataRate is the bit rate
	FDev       int    `json:"f_dev,omitempty"`      // FSK frequency deviation, kHz
}

// Event is published on the event bus, stored, and shown in live data.
type Event struct {
	ID       int64     `json:"id"`
	Name     string    `json:"name"` // as.up, ns.join, gs.up, gs.status, gs.down, as.down.queued ...
	Time     time.Time `json:"time"`
	AppID    string    `json:"application_id,omitempty"`
	DevID    string    `json:"device_id,omitempty"`
	Gateways []string  `json:"gateway_ids,omitempty"`
	Summary  string    `json:"summary"`
	Data     any       `json:"data"`
}
