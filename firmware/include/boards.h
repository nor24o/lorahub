// Pins of the supported boards. platformio.ini picks one with -D BOARD_...;
// any pin can still be changed with its own -D, for example -D RELAY_PIN=5.
#pragma once

#if defined(BOARD_HELTEC_V3)
// Heltec WiFi LoRa 32 V3: ESP32-S3 and SX1262.
#define RADIO_SX1262 1
#define BOARD_LORA_SCK 9
#define BOARD_LORA_MISO 11
#define BOARD_LORA_MOSI 10
#define BOARD_LORA_NSS 8
#define BOARD_LORA_RST 12
#define BOARD_LORA_IRQ 14  // DIO1
#define BOARD_LORA_BUSY 13
#define BOARD_TCXO_VOLTAGE 1.8
#define BOARD_DIO2_RF_SWITCH 1
#define BOARD_LED 35
#define BOARD_BUTTON 0     // PRG button
#define BOARD_VBAT_PIN 1
#define BOARD_VBAT_DIVIDER 4.9f   // 390k / 100k
#define BOARD_VBAT_ENABLE_PIN 37  // ADC_Ctrl
#define BOARD_VBAT_ENABLE_LEVEL 0 // some later revisions need 1: change it if the battery reads 0 V
#define BOARD_RELAY_PIN 7
#define BOARD_ONEWIRE_PIN 6
#define BOARD_I2C_SDA 41
#define BOARD_I2C_SCL 42

#elif defined(BOARD_TTGO_LORA32_V21)
// LilyGO TTGO LoRa32 V2.1 (T3 V1.6.1): ESP32 and SX1276.
#define RADIO_SX1276 1
#define BOARD_LORA_SCK 5
#define BOARD_LORA_MISO 19
#define BOARD_LORA_MOSI 27
#define BOARD_LORA_NSS 18
#define BOARD_LORA_RST 23
#define BOARD_LORA_IRQ 26  // DIO0
#define BOARD_LORA_DIO1 33
#define BOARD_LED 25
#define BOARD_BUTTON -1    // no user button: wire one to BUTTON_PIN
#define BOARD_VBAT_PIN 35
#define BOARD_VBAT_DIVIDER 2.0f
#define BOARD_RELAY_PIN 13
#define BOARD_ONEWIRE_PIN 4
#define BOARD_I2C_SDA 21
#define BOARD_I2C_SCL 22

#elif defined(BOARD_ESP32_RFM95)
// Any ESP32 board with an RFM95 / SX1276 module, wired as in docs/hardware.md.
#define RADIO_SX1276 1
#define BOARD_LORA_SCK 5
#define BOARD_LORA_MISO 19
#define BOARD_LORA_MOSI 27
#define BOARD_LORA_NSS 18
#define BOARD_LORA_RST 14
#define BOARD_LORA_IRQ 26  // DIO0
#define BOARD_LORA_DIO1 33
#define BOARD_LED 2
#define BOARD_BUTTON 0     // BOOT button
#define BOARD_VBAT_PIN -1  // no battery divider unless you add one (docs/hardware.md)
#define BOARD_RELAY_PIN 25
#define BOARD_ONEWIRE_PIN 4
#define BOARD_I2C_SDA 21
#define BOARD_I2C_SCL 22

#elif defined(BOARD_CUSTOM)
// Your own board: give every pin with -D (see nodes/example-custom-board.ini).
#if !defined(RADIO_SX1262) && !defined(RADIO_SX1276)
#error "BOARD_CUSTOM: add -D RADIO_SX1262=1 or -D RADIO_SX1276=1"
#endif

#else
#error "No board chosen: build one of the environments in platformio.ini"
#endif

// Defaults for anything the board does not have.
#ifndef BOARD_LORA_BUSY
#define BOARD_LORA_BUSY -1
#endif
#ifndef BOARD_LORA_DIO1
#define BOARD_LORA_DIO1 -1
#endif
#ifndef BOARD_TCXO_VOLTAGE
#define BOARD_TCXO_VOLTAGE 0.0
#endif
#ifndef BOARD_DIO2_RF_SWITCH
#define BOARD_DIO2_RF_SWITCH 0
#endif
#ifndef BOARD_LED
#define BOARD_LED -1
#endif
#ifndef BOARD_BUTTON
#define BOARD_BUTTON -1
#endif
#ifndef BOARD_VBAT_PIN
#define BOARD_VBAT_PIN -1
#endif
#ifndef BOARD_VBAT_DIVIDER
#define BOARD_VBAT_DIVIDER 2.0f
#endif
#ifndef BOARD_VBAT_ENABLE_PIN
#define BOARD_VBAT_ENABLE_PIN -1
#endif
#ifndef BOARD_VBAT_ENABLE_LEVEL
#define BOARD_VBAT_ENABLE_LEVEL 1
#endif
#ifndef BOARD_RELAY_PIN
#define BOARD_RELAY_PIN -1
#endif
#ifndef BOARD_ONEWIRE_PIN
#define BOARD_ONEWIRE_PIN -1
#endif
#ifndef BOARD_I2C_SDA
#define BOARD_I2C_SDA 21
#endif
#ifndef BOARD_I2C_SCL
#define BOARD_I2C_SCL 22
#endif
