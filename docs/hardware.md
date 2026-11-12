# Boards and wiring

Use an 868 MHz board and always connect the antenna before switching on: a
radio sending without one can be damaged.

## Heltec WiFi LoRa 32 V3 (`heltec_v3_*`)

ESP32-S3 with an SX1262 on the board. Nothing to wire for the radio.

| Part | Pin |
|---|---|
| LED | 35 (white LED) |
| Button | 0 (PRG) |
| Battery | 1, through the board's divider (switched by pin 37) |
| Relay output | 7 |
| DS18B20 data | 6 |
| BME280 SDA / SCL | 41 / 42 |

If the battery reads 0 V, the board is a revision whose divider switch works
the other way: add `-D BATTERY_ENABLE_LEVEL=1`.

## LilyGO TTGO LoRa32 V2.1 (`ttgo_lora32_*`)

ESP32 with an SX1276 on the board.

| Part | Pin |
|---|---|
| LED | 25 |
| Button | none on the board: the button and counter builds use pin 0 (BOOT) |
| Battery | 35, divider 2:1 on the board |
| Relay output | 13 |
| DS18B20 data | 4 |
| BME280 SDA / SCL | 21 / 22 |

## Any ESP32 with an RFM95 / SX1276 module (`esp32_rfm95_*`)

| RFM95 | ESP32 |
|---|---|
| VCC | 3.3 V (not 5 V) |
| GND | GND |
| SCK | 5 |
| MISO | 19 |
| MOSI | 27 |
| NSS | 18 |
| RESET | 14 |
| DIO0 | 26 |
| DIO1 | 33 (optional) |
| ANT | antenna, 8.2 cm wire for 868 MHz if nothing else |

| Part | Pin |
|---|---|
| LED | 2 |
| Button | 0 (BOOT) |
| Relay output | 25 |
| DS18B20 data | 4 |
| BME280 SDA / SCL | 21 / 22 |
| Battery | none; for one, add a 100 kΩ + 100 kΩ divider from the battery to pin 35 and build with `-D BATTERY_PIN=35 -D BATTERY_DIVIDER=2.0` |

## Other boards

`firmware/nodes/example-custom-board.ini` shows an ESP32-C3 with an SX1262
module: `-D BOARD_CUSTOM`, the chip (`RADIO_SX1262=1` or `RADIO_SX1276=1`) and
every pin. SX1262 modules usually need `TCXO_VOLTAGE=1.8` and, when the
antenna switch is on DIO2, `DIO2_RF_SWITCH=1`.

Battery nodes (sensor, button, counter) sleep between reports and wake from
their button or pulse input, which must then be an RTC pin: on the ESP32 0, 2,
4, 12-15, 25-27, 32-39; on the ESP32-S3 0-21. The ESP32-C3 has no such pins:
use it for relays.

## Parts

| Part | Connection |
|---|---|
| DS18B20 | data to the DS18B20 pin, 4.7 kΩ from data to 3.3 V, GND, 3.3 V |
| BME280 | SDA, SCL, 3.3 V, GND; address 0x76 (SDO to GND) or 0x77 (`-D BME280_ADDRESS=0x77`) |
| Relay | a relay module with its own driver transistor; its IN to the relay pin. Modules that switch on with a low input: `-D RELAY_ACTIVE_HIGH=0` |
| Button | between the button pin and GND (the internal pull-up is used) |
| Meter pulse output | reed contact or open collector between the pulse pin and GND |
| Battery | one Li-ion cell on the board's battery connector |
