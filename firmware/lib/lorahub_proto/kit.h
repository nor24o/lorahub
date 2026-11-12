// The node kit's own conventions on top of the LoRaHub protocol: which Cayenne
// LPP channel carries which value, the command codes, and the commands each
// node type announces. decoder/lorahub-node-kit.js reads the same channels.
// Plain C++, tested on a PC with firmware/test/host.
#pragma once

#include "lorahub_proto.h"

namespace kit {

enum Type : uint8_t { kSensor = 1, kRelay = 2, kButton = 3, kCounter = 4 };

// Cayenne LPP channels. The decoder names the values after these.
enum Channel : uint8_t {
  kChTemperature = 1, // temperature, °C
  kChHumidity = 2,    // humidity, %
  kChPressure = 3,    // pressure, hPa
  kChBattery = 4,     // battery, V
  kChRelay = 5,       // relay, ON or OFF
  kChPresses = 6,     // presses, button presses since power-on
  kChCount = 7,       // count, pulses counted (kept across sleep and restarts)
  kChInterval = 8,    // interval, s between reports
  kChButton = 9,      // button, 1 when this report was sent because of a press
};

// Command codes: the first data byte of a downlink. FF asks the node to
// describe itself.
enum Command : uint8_t {
  kCmdRelay = 0x01,      // 01 01 on, 01 00 off
  kCmdPulse = 0x02,      // 02 + u16 seconds: on, then off after that time
  kCmdInterval = 0x03,   // 03 + u16 seconds between reports
  kCmdIdentify = 0x04,   // 04: blink the LED for a few seconds
  kCmdResetCount = 0x05, // 05: set the pulse count to 0
  kCmdRestart = 0x06,    // 06: restart the node
};

// What the node can do, decided at build time.
struct Features {
  Type type = kSensor;
  bool led = false;
  uint16_t interval_min_s = 30;
  uint16_t interval_max_s = 43200;
  uint16_t pulse_max_s = 3600;
};

// One report. Only the values marked present are sent.
struct Readings {
  bool has_temperature = false;
  float temperature = 0;
  bool has_humidity = false;
  float humidity = 0;
  bool has_pressure = false;
  float pressure = 0;
  bool has_battery = false;
  float battery_v = 0;
  bool has_relay = false;
  bool relay = false;
  bool has_presses = false;
  uint32_t presses = 0;
  bool has_count = false;
  uint32_t count = 0;
  bool has_interval = false;
  uint16_t interval_s = 0;
  bool pressed = false; // this report was sent because of a button press
};

// The data of a report (after the header).
void writeReadings(lorahub::Writer &w, const Readings &r);

// Largest packet (header included) at this spreading factor, EU868.
size_t maxPacket(uint8_t sf);

// The self-description of a node, split into as few packets as fit
// max_packet bytes each. Returns the number of packets written to
// out[0..max_parts-1] (each kMaxDescribePacket bytes, length in lens[]), or 0
// when an entry does not fit at all.
constexpr size_t kMaxDescribePacket = 128;
size_t describe(const lorahub::Identity &id, const lorahub::Profile &p, const Features &f, size_t max_packet,
                uint8_t out[][kMaxDescribePacket], size_t lens[], size_t max_parts);

} // namespace kit
