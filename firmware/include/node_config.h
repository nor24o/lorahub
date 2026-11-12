// Every build-time setting of a node, with its default. Change any of them in
// platformio.ini or in your own file in nodes/, as -D NAME=value; there is no
// need to edit this file. docs/configuration.md explains each one.
#pragma once

#include "boards.h"

#define NODE_SENSOR 1  // battery sensor, class A: temperature, humidity, pressure, battery
#define NODE_RELAY 2   // mains relay, class C: switch, timed pulse, local button
#define NODE_BUTTON 3  // battery button, class A: a report for every press, and a heartbeat
#define NODE_COUNTER 4 // battery pulse counter, class A: water, gas or power meters

// ---------- who the node is ----------
#ifndef NODE_TYPE
#define NODE_TYPE NODE_SENSOR
#endif
#ifndef NODE_ID          // the node group in LoRaHub: all nodes of one kind share it
#define NODE_ID 0x0A01
#endif
#ifndef NODE_SERIAL      // this node; 0 = made from the chip's factory MAC address (printed at start)
#define NODE_SERIAL 0
#endif
#ifndef NODE_CLASS       // 'A' sleeps and listens only after sending; 'C' listens all the time
#if NODE_TYPE == NODE_RELAY
#define NODE_CLASS 'C'
#else
#define NODE_CLASS 'A'
#endif
#endif

// ---------- uplink radio: must be one the gateways listen on ----------
#ifndef UPLINK_FREQ_MHZ
#define UPLINK_FREQ_MHZ 868.1
#endif
#ifndef UPLINK_HOP       // 1 = take the gateways' 8 channels in turn (867.1-868.5 MHz)
#define UPLINK_HOP 0
#endif
#ifndef UPLINK_SF        // 7 (short range, little airtime) to 12 (long range)
#define UPLINK_SF 9
#endif
#ifndef UPLINK_BW_KHZ
#define UPLINK_BW_KHZ 125
#endif
#ifndef UPLINK_CR        // 5 to 8 = coding rate 4/5 to 4/8
#define UPLINK_CR 5
#endif
#ifndef TX_POWER_DBM
#define TX_POWER_DBM 14
#endif
#ifndef LORA_PREAMBLE
#define LORA_PREAMBLE 8
#endif
#ifndef LORA_SYNC_WORD   // 0x12 = "private", as the LoRaHub gateways are set
#define LORA_SYNC_WORD 0x12
#endif

// ---------- downlinks: must match the node group's settings in LoRaHub ----------
#ifndef SEND_PROFILE     // group option "Nodes send a profile after SERIAL_ID"
#define SEND_PROFILE 1
#endif
#ifndef DOWNLINK_HEADER  // group option "Prepend NODE_ID and SERIAL_ID"
#define DOWNLINK_HEADER 1
#endif
#ifndef DOWNLINK_CONTROL // group option "Control byte"
#define DOWNLINK_CONTROL 1
#endif
#ifndef RX_INVERT_IQ     // 1 = the node listens with inverted IQ (announced in the profile)
#define RX_INVERT_IQ 0
#endif
#ifndef RX_DELAY_MS      // class A: from the end of an uplink to the downlink (LoRaHub's default)
#define RX_DELAY_MS 1000
#endif
#ifndef RX_MARGIN_MS     // class A: listen this much earlier and longer
#define RX_MARGIN_MS 40
#endif
#ifndef MAX_DOWNLINK     // longest downlink the node waits for, bytes
#define MAX_DOWNLINK 32
#endif
// Where the node listens. 0 = on the uplink's frequency and spreading factor.
// Anything else is announced in the profile's radio block, so LoRaHub sends
// there without any setting on the hub. Class C nodes need a fixed one.
#ifndef RX_FREQ_MHZ
#if NODE_CLASS == 'C'
#define RX_FREQ_MHZ 869.525
#else
#define RX_FREQ_MHZ 0
#endif
#endif
#ifndef RX_SF
#if NODE_CLASS == 'C'
#define RX_SF 9
#else
#define RX_SF 0
#endif
#endif
#ifndef RX_BW_KHZ
#define RX_BW_KHZ 125
#endif
#ifndef RX_CR
#define RX_CR 5
#endif

// ---------- reports ----------
#ifndef REPORT_INTERVAL_S // between reports; the Interval command changes it (kept across restarts)
#if NODE_TYPE == NODE_BUTTON
#define REPORT_INTERVAL_S 3600
#elif NODE_TYPE == NODE_RELAY
#define REPORT_INTERVAL_S 900
#else
#define REPORT_INTERVAL_S 300
#endif
#endif
#ifndef INTERVAL_MIN_S
#define INTERVAL_MIN_S 30
#endif
#ifndef INTERVAL_MAX_S
#define INTERVAL_MAX_S 43200
#endif
#ifndef DESCRIBE_ON_BOOT  // send the list of commands after power-on (and whenever the hub asks)
#define DESCRIBE_ON_BOOT 1
#endif
#ifndef DUTY_CYCLE_PCT    // EU868: 1 % of the time on air in 868.0-868.6 MHz
#define DUTY_CYCLE_PCT 1
#endif

// ---------- pins and parts ----------
#ifndef LORA_SCK
#define LORA_SCK BOARD_LORA_SCK
#endif
#ifndef LORA_MISO
#define LORA_MISO BOARD_LORA_MISO
#endif
#ifndef LORA_MOSI
#define LORA_MOSI BOARD_LORA_MOSI
#endif
#ifndef LORA_NSS
#define LORA_NSS BOARD_LORA_NSS
#endif
#ifndef LORA_RST
#define LORA_RST BOARD_LORA_RST
#endif
#ifndef LORA_IRQ          // DIO0 on SX1276, DIO1 on SX1262
#define LORA_IRQ BOARD_LORA_IRQ
#endif
#ifndef LORA_BUSY         // SX1262 only
#define LORA_BUSY BOARD_LORA_BUSY
#endif
#ifndef LORA_DIO1         // SX1276 only
#define LORA_DIO1 BOARD_LORA_DIO1
#endif
#ifndef TCXO_VOLTAGE      // SX1262 boards with a TCXO, usually 1.8; 0 = crystal
#define TCXO_VOLTAGE BOARD_TCXO_VOLTAGE
#endif
#ifndef DIO2_RF_SWITCH    // SX1262 boards whose antenna switch is on DIO2
#define DIO2_RF_SWITCH BOARD_DIO2_RF_SWITCH
#endif

#ifndef LED_PIN           // -1 = none (no Identify command)
#define LED_PIN BOARD_LED
#endif
#ifndef BUTTON_PIN        // button type: the button; relay type: toggles the relay; counter: see PULSE_PIN
#define BUTTON_PIN BOARD_BUTTON
#endif
#ifndef BUTTON_ACTIVE_LOW // 1 = pressed connects the pin to GND
#define BUTTON_ACTIVE_LOW 1
#endif
#ifndef RELAY_PIN
#define RELAY_PIN BOARD_RELAY_PIN
#endif
#ifndef RELAY_ACTIVE_HIGH
#define RELAY_ACTIVE_HIGH 1
#endif
#ifndef RELAY_RESTORE     // 1 = after a power cut the relay returns to its last state; 0 = starts off
#define RELAY_RESTORE 1
#endif
#ifndef PULSE_MAX_S       // longest timed pulse the Pulse command accepts
#define PULSE_MAX_S 3600
#endif
#ifndef PULSE_PIN         // counter type: the meter's pulse output (reed contact or open collector)
#define PULSE_PIN BUTTON_PIN
#endif
#ifndef PULSE_ACTIVE_LOW
#define PULSE_ACTIVE_LOW BUTTON_ACTIVE_LOW
#endif

#ifndef SENSOR_DS18B20    // 1 = DS18B20 on ONEWIRE_PIN (4.7k pull-up to 3.3 V)
#define SENSOR_DS18B20 0
#endif
#ifndef ONEWIRE_PIN
#define ONEWIRE_PIN BOARD_ONEWIRE_PIN
#endif
#ifndef SENSOR_BME280     // 1 = BME280 on I2C: temperature, humidity, pressure
#define SENSOR_BME280 0
#endif
#ifndef BME280_ADDRESS
#define BME280_ADDRESS 0x76
#endif
#ifndef I2C_SDA
#define I2C_SDA BOARD_I2C_SDA
#endif
#ifndef I2C_SCL
#define I2C_SCL BOARD_I2C_SCL
#endif

#ifndef BATTERY_PIN       // -1 = no battery reading
#define BATTERY_PIN BOARD_VBAT_PIN
#endif
#ifndef BATTERY_DIVIDER   // battery voltage = pin voltage × this
#define BATTERY_DIVIDER BOARD_VBAT_DIVIDER
#endif
#ifndef BATTERY_ENABLE_PIN
#define BATTERY_ENABLE_PIN BOARD_VBAT_ENABLE_PIN
#endif
#ifndef BATTERY_ENABLE_LEVEL
#define BATTERY_ENABLE_LEVEL BOARD_VBAT_ENABLE_LEVEL
#endif

#ifndef DEBUG_SERIAL      // 1 = log to the USB serial port at 115200 baud
#define DEBUG_SERIAL 1
#endif

// ---------- checks ----------
#if NODE_TYPE < NODE_SENSOR || NODE_TYPE > NODE_COUNTER
#error "NODE_TYPE must be NODE_SENSOR, NODE_RELAY, NODE_BUTTON or NODE_COUNTER"
#endif
#if NODE_TYPE == NODE_RELAY && NODE_CLASS != 'C'
#error "A relay node listens all the time: NODE_CLASS must be 'C'"
#endif
#if NODE_TYPE != NODE_RELAY && NODE_CLASS == 'C'
#error "Only the relay type is a class C node; the others sleep (class A)"
#endif
#if NODE_TYPE == NODE_RELAY && RELAY_PIN < 0
#error "Set RELAY_PIN for this board"
#endif
#if NODE_TYPE == NODE_BUTTON && BUTTON_PIN < 0
#error "Set BUTTON_PIN for this board"
#endif
#if NODE_TYPE == NODE_COUNTER && PULSE_PIN < 0
#error "Set PULSE_PIN for this board"
#endif
#if SENSOR_DS18B20 && ONEWIRE_PIN < 0
#error "Set ONEWIRE_PIN for the DS18B20"
#endif
#if NODE_ID == 0xFFFF || NODE_ID == 0
#error "NODE_ID 0000 and FFFF are reserved"
#endif
// (A class C node also needs a fixed RX_FREQ_MHZ: checked in main.cpp.)
#if !DOWNLINK_HEADER && NODE_CLASS == 'C'
#error "A class C node hears other nodes' packets: keep DOWNLINK_HEADER on"
#endif
