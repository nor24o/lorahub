// LoRaHub node protocol: the bytes a node sends to LoRaHub and checks in what
// it receives. Plain C++ with no Arduino or radio code, so it also builds and
// is tested on a PC (see firmware/test/host).
//
// Uplink:   NODE_ID (2) | SERIAL_ID (4) | [profile (1) | radio block (4)] | data
// Downlink: NODE_ID (2) | SERIAL_ID (4) | [control (1)] | command
// Every value is most significant byte first. Full reference: docs/protocol.md.
#pragma once

#include <stddef.h>
#include <stdint.h>

namespace lorahub {

// The two numbers that identify a node. NODE_ID picks the node group in
// LoRaHub, SERIAL_ID the node inside it.
struct Identity {
  uint16_t node_id;
  uint32_t serial;
};

// What the node tells LoRaHub about itself in the profile byte, and how it
// listens for downlinks (the radio block).
struct Profile {
  char cls = 'A';           // 'A', 'B' or 'C'
  bool invert_iq = false;   // the node listens with inverted IQ
  bool radio_block = false; // send the 4-byte radio block
  uint32_t rx_freq_hz = 0;  // 0 = the uplink's frequency
  uint8_t rx_sf = 0;        // 5-12, 0 = the uplink's
  uint16_t rx_bw_khz = 125; // 125, 250 or 500
  uint8_t rx_cr = 5;        // 5-8 for 4/5 to 4/8
  uint8_t user = 0;         // bits 2-0, free for the firmware
};

// Profile bits.
constexpr uint8_t kProfileClassA = 0x00;
constexpr uint8_t kProfileClassB = 0x40;
constexpr uint8_t kProfileClassC = 0x80;
constexpr uint8_t kProfileInvertIQ = 0x20;
constexpr uint8_t kProfileRadioBlock = 0x10;
constexpr uint8_t kProfileDescribe = 0x08;

constexpr uint8_t kDescribeFormat = 0xD1;  // description, format 1
constexpr uint8_t kDescribeRequest = 0xFF; // downlink: "describe yourself"
constexpr uint16_t kAnyNode = 0xFFFF;
constexpr uint32_t kAnySerial = 0xFFFFFFFF;

// Writer fills a buffer and remembers when it ran out of room, so a packet
// that does not fit is never sent half written.
class Writer {
 public:
  Writer(uint8_t *buf, size_t cap) : buf_(buf), cap_(cap) {}
  void u8(uint8_t v);
  void u16(uint16_t v);
  void u24(uint32_t v);
  void u32(uint32_t v);
  void bytes(const uint8_t *p, size_t n);
  void text(const char *s, size_t max_len); // length byte, then the characters
  size_t size() const { return n_; }
  bool ok() const { return !overflow_; }
  const uint8_t *data() const { return buf_; }

 private:
  uint8_t *buf_;
  size_t cap_;
  size_t n_ = 0;
  bool overflow_ = false;
};

uint8_t profileByte(const Profile &p, bool describe);

// Header of every uplink: NODE_ID and SERIAL_ID, then, when with_profile is
// set, the profile byte and the radio block if the profile asks for it.
void uplinkHeader(Writer &w, const Identity &id, const Profile *p, bool with_profile, bool describe = false);

// Cayenne LPP values: channel, type, value. LoRaHub's Cayenne LPP decoder
// reads these as "<type>_<channel>"; the kit's JavaScript decoder
// (decoder/lorahub-node-kit.js) names them by channel instead.
void lppTemperature(Writer &w, uint8_t ch, float celsius);  // 0.1 °C
void lppHumidity(Writer &w, uint8_t ch, float percent);     // 0.5 %
void lppPressure(Writer &w, uint8_t ch, float hpa);         // 0.1 hPa
void lppVoltage(Writer &w, uint8_t ch, float volts);        // 0.01 V
void lppDigitalIn(Writer &w, uint8_t ch, uint8_t v);
void lppDigitalOut(Writer &w, uint8_t ch, uint8_t v);
void lppGeneric(Writer &w, uint8_t ch, uint32_t v);         // unsigned, 4 bytes
void lppPercentage(Writer &w, uint8_t ch, uint8_t v);

// Value formats of described commands.
enum Format : uint8_t { kNone = 0, kU8 = 1, kI8 = 2, kU16 = 3, kI16 = 4, kU32 = 5, kI32 = 6 };

// Description builds a self-description uplink: the commands the node accepts.
// LoRaHub turns them into buttons, switches, sliders and lists, in the console
// and in Home Assistant. Names: 1-24 ASCII characters; keep them short.
class Description {
 public:
  // A whole description packet: header, D1, part byte, then the entries.
  Description(Writer &w, const Identity &id, const Profile &p, uint8_t part = 1, uint8_t parts = 1);
  // Entries only, for packing a long list into several parts.
  explicit Description(Writer &w) : w_(w) {}
  static void header(Writer &w, const Identity &id, const Profile &p, uint8_t part, uint8_t parts);
  void button(uint8_t code, const char *name);                          // downlink: code
  void buttonWithValue(uint8_t code, Format f, int32_t value, const char *name); // code value
  void toggle(uint8_t code, const char *name);                          // code 01 on, code 00 off
  void number(uint8_t code, Format f, int32_t min, int32_t max, int8_t scale, const char *name, const char *unit);
  void choiceBegin(uint8_t code, Format f, uint8_t count, const char *name); // then count options
  void option(int32_t value, const char *name);
  void raw(uint8_t code, uint8_t max_len, const char *name);            // code bytes
  bool ok() const { return w_.ok(); }

 private:
  void value(Format f, int32_t v);
  Writer &w_;
  Format choice_format_ = kNone;
  const char *choice_name_ = nullptr;
  uint8_t choice_left_ = 0;
};

// Audience of a downlink (control byte bits 6-5).
enum Audience : uint8_t { kThisNode = 0, kGroup = 1, kEveryone = 2 };

struct Downlink {
  Audience audience = kThisNode;
  bool has_control = false;
  bool confirm = false; // the hub asks for an uplink back
  uint8_t seq = 0;      // 0-15
  const uint8_t *data = nullptr; // the command: code, then its value
  size_t len = 0;
};

// Checks a received packet and finds the command in it. header and control
// must match the group's settings in LoRaHub ("Prepend NODE_ID and
// SERIAL_ID", "Control byte"). Returns false for a packet that is not for
// this node: another node's uplink, another group, or a malformed control byte.
bool parseDownlink(const uint8_t *b, size_t n, const Identity &id, bool header, bool control, Downlink *out);

// RepeatFilter drops a downlink the node has just handled: a broadcast goes
// out once through every gateway with the same sequence number.
class RepeatFilter {
 public:
  // True when this downlink was already handled within window_ms.
  bool repeated(const Downlink &d, uint32_t now_ms, uint32_t window_ms = 30000);

 private:
  int16_t last_seq_[3] = {-1, -1, -1};
  uint32_t last_ms_[3] = {0, 0, 0};
};

// Airtime of a LoRa packet in microseconds (explicit header, CRC on).
uint32_t airtimeUs(size_t len, uint8_t sf, uint16_t bw_khz, uint8_t cr, uint16_t preamble);

} // namespace lorahub
