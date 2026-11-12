# Build-time settings

Each setting is a `-D NAME=value` line in a node's `build_flags`, in
`firmware/platformio.ini` or in your own file in `firmware/nodes/`. Anything
not set keeps the default in `firmware/include/node_config.h`. Wrong
combinations stop the build with a message saying what to change.

## Who the node is

| Setting | Default | Meaning |
|---|---|---|
| `NODE_TYPE` | `NODE_SENSOR` | `NODE_SENSOR`, `NODE_RELAY`, `NODE_BUTTON` or `NODE_COUNTER` (set by the ready builds) |
| `NODE_ID` | `0x0A01` | The node group in LoRaHub. One per kind of node; `0000` and `FFFF` are reserved |
| `NODE_SERIAL` | `0` | This node. `0` = the last 4 bytes of the chip's MAC address, printed at start. Set it to keep a node's identity when you replace its board |
| `NODE_CLASS` | `'A'`, relay `'C'` | Set by the type: sensors, buttons and counters sleep (A), a relay listens all the time (C) |

## Uplink radio

These must be settings the gateways listen on: EU868 channels 867.1, 867.3,
867.5, 867.7, 867.9, 868.1, 868.3, 868.5 MHz, 125 kHz, SF7 to SF12, private
sync word. See [radio.md](radio.md).

| Setting | Default | Meaning |
|---|---|---|
| `UPLINK_FREQ_MHZ` | `868.1` | Frequency of the reports |
| `UPLINK_HOP` | `0` | `1` = use the 8 channels in turn (spreads the duty cycle) |
| `UPLINK_SF` | `9` | Spreading factor 7-12: higher reaches farther but takes longer on air |
| `UPLINK_BW_KHZ` | `125` | Bandwidth |
| `UPLINK_CR` | `5` | Coding rate 4/5 (`5`) to 4/8 (`8`) |
| `TX_POWER_DBM` | `14` | Transmit power; 14 dBm is the EU868 limit on these channels |
| `LORA_PREAMBLE` | `8` | Preamble symbols, as the gateways use |
| `LORA_SYNC_WORD` | `0x12` | Private sync word, as the gateways are set (`sync_word: private`) |
| `DUTY_CYCLE_PCT` | `1` | Share of each hour the node may send. The shortest interval follows from it (SF9: 30 s, SF12: about 180 s) |

## Downlinks

The first four must match the node group's options in LoRaHub
([lorahub-setup.md](lorahub-setup.md)).

| Setting | Default | Meaning |
|---|---|---|
| `SEND_PROFILE` | `1` | Send the profile byte after SERIAL_ID. Group option "Nodes send a profile after SERIAL_ID" |
| `DOWNLINK_HEADER` | `1` | Downlinks start with NODE_ID and SERIAL_ID. Group option "Prepend NODE_ID + SERIAL_ID to downlinks". A class C node needs it |
| `DOWNLINK_CONTROL` | `1` | Downlinks carry a control byte. Group option "Add a control byte after SERIAL_ID in downlinks" |
| `RX_INVERT_IQ` | `0` | `1` = listen with inverted IQ. Announced in the profile, so LoRaHub sends that way; the gateways and other nodes then do not hear the downlinks as packets |
| `RX_FREQ_MHZ` | `0`, relay `869.525` | Where the node listens. `0` = on the uplink's frequency. Anything else is announced in the profile's radio block |
| `RX_SF` | `0`, relay `9` | Spreading factor it listens on; `0` = the uplink's |
| `RX_BW_KHZ` | `125` | Bandwidth it listens on, when `RX_FREQ_MHZ` or `RX_SF` is set |
| `RX_CR` | `5` | Coding rate announced in the radio block |
| `RX_DELAY_MS` | `1000` | Class A: time from the end of a report to its downlink. LoRaHub's default is 1 s |
| `RX_MARGIN_MS` | `40` | Class A: how much earlier the node starts listening, and how much longer |
| `MAX_DOWNLINK` | `32` | Longest command (bytes after the header) the node waits for |

## Reports

| Setting | Default | Meaning |
|---|---|---|
| `REPORT_INTERVAL_S` | sensor and counter `300`, relay `900`, button `3600` | Time between reports. The Interval command changes it; the new value is kept in flash |
| `INTERVAL_MIN_S` | `30` | Smallest interval the Interval command accepts (the duty cycle may raise it) |
| `INTERVAL_MAX_S` | `43200` | Largest one, 12 hours |
| `DESCRIBE_ON_BOOT` | `1` | Send the list of commands after power-on. LoRaHub can always ask for it again (`FF`) |
| `REPORT_BATTERY` | `1` when the board has a battery pin, `0` for relays | Include the battery voltage |

## Pins and parts

The board sets every pin (`firmware/include/boards.h`); change any one here.

| Setting | Meaning |
|---|---|
| `LORA_SCK`, `LORA_MISO`, `LORA_MOSI`, `LORA_NSS`, `LORA_RST` | Radio SPI and reset |
| `LORA_IRQ` | DIO0 on an SX1276, DIO1 on an SX1262 |
| `LORA_BUSY` | SX1262 BUSY |
| `LORA_DIO1` | SX1276 DIO1 (optional) |
| `RADIO_SX1262=1` or `RADIO_SX1276=1` | The radio chip, for `BOARD_CUSTOM` |
| `TCXO_VOLTAGE` | SX1262 with a TCXO, usually `1.8`; `0` = crystal |
| `DIO2_RF_SWITCH` | `1` = the SX1262 switches the antenna with DIO2 |
| `LED_PIN` | `-1` = none; without an LED there is no Identify command |
| `BUTTON_PIN`, `BUTTON_ACTIVE_LOW` | Button type: the button. Relay type: toggles the relay (`-1` = none) |
| `RELAY_PIN`, `RELAY_ACTIVE_HIGH` | Relay output |
| `RELAY_RESTORE` | `1` = after a power cut the relay returns to its last state; `0` = it starts off |
| `PULSE_MAX_S` | Longest Pulse the relay accepts, default 3600 s |
| `PULSE_PIN`, `PULSE_ACTIVE_LOW` | Counter type: the meter's pulse output. Must be an RTC pin (it wakes the chip) |
| `SENSOR_DS18B20=1`, `ONEWIRE_PIN` | DS18B20 with a 4.7 kΩ pull-up to 3.3 V |
| `SENSOR_BME280=1`, `BME280_ADDRESS`, `I2C_SDA`, `I2C_SCL` | BME280 on I2C, address `0x76` or `0x77` |
| `BATTERY_PIN`, `BATTERY_DIVIDER` | ADC pin and divider ratio: battery voltage = pin voltage × ratio |
| `BATTERY_ENABLE_PIN`, `BATTERY_ENABLE_LEVEL` | A pin that switches the divider on while reading (Heltec V3: pin 37) |
| `DEBUG_SERIAL` | `1` = log every packet to USB serial at 115200 baud; `0` saves a little power |

## Examples

A relay that also sends its state every 5 minutes and listens on 869.525 MHz
SF10:

```ini
[env:pump]
extends = env:heltec_v3_relay
build_flags = ${env:heltec_v3_relay.build_flags}
  -D NODE_ID=0xA301
  -D REPORT_INTERVAL_S=300
  -D RX_SF=10
```

A water meter on pin 4, reporting every 15 minutes at SF10:

```ini
[env:water]
extends = env:esp32_rfm95_counter
build_flags = ${env:esp32_rfm95_counter.build_flags}
  -D NODE_ID=0xA401
  -D PULSE_PIN=4
  -D REPORT_INTERVAL_S=900
  -D UPLINK_SF=10
```
