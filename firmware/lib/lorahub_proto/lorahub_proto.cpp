#include "lorahub_proto.h"

#include <string.h>

namespace lorahub {

// ---------- Writer ----------

void Writer::u8(uint8_t v) {
  if (n_ >= cap_) {
    overflow_ = true;
    return;
  }
  buf_[n_++] = v;
}

void Writer::u16(uint16_t v) {
  u8(uint8_t(v >> 8));
  u8(uint8_t(v));
}

void Writer::u24(uint32_t v) {
  u8(uint8_t(v >> 16));
  u8(uint8_t(v >> 8));
  u8(uint8_t(v));
}

void Writer::u32(uint32_t v) {
  u16(uint16_t(v >> 16));
  u16(uint16_t(v));
}

void Writer::bytes(const uint8_t *p, size_t n) {
  for (size_t i = 0; i < n; i++) u8(p[i]);
}

void Writer::text(const char *s, size_t max_len) {
  size_t n = s ? strlen(s) : 0;
  if (n > max_len) n = max_len;
  u8(uint8_t(n));
  bytes(reinterpret_cast<const uint8_t *>(s), n);
}

// ---------- profile and header ----------

static uint8_t bwCode(uint16_t khz) {
  switch (khz) {
    case 250: return 1;
    case 500: return 2;
    default: return 0; // 125 kHz
  }
}

uint8_t profileByte(const Profile &p, bool describe) {
  uint8_t b = kProfileClassA;
  if (p.cls == 'B' || p.cls == 'b') b = kProfileClassB;
  if (p.cls == 'C' || p.cls == 'c') b = kProfileClassC;
  if (p.invert_iq) b |= kProfileInvertIQ;
  if (p.radio_block) b |= kProfileRadioBlock;
  if (describe) b |= kProfileDescribe;
  return b | (p.user & 0x07);
}

void uplinkHeader(Writer &w, const Identity &id, const Profile *p, bool with_profile, bool describe) {
  w.u16(id.node_id);
  w.u32(id.serial);
  if (!with_profile && !describe) return;
  Profile none;
  const Profile &pr = p ? *p : none;
  w.u8(profileByte(pr, describe));
  if (pr.radio_block) {
    w.u24(pr.rx_freq_hz / 100); // frequency in 100 Hz, 0 = the uplink's
    uint8_t sf = pr.rx_sf <= 12 ? pr.rx_sf : 0;
    uint8_t cr = pr.rx_cr >= 5 && pr.rx_cr <= 8 ? uint8_t(pr.rx_cr - 5) : 0;
    w.u8(uint8_t(sf << 4 | bwCode(pr.rx_bw_khz) << 2 | cr));
  }
}

// ---------- Cayenne LPP ----------

static int32_t roundTo(float v, float step) {
  float x = v / step;
  return int32_t(x < 0 ? x - 0.5f : x + 0.5f);
}

void lppTemperature(Writer &w, uint8_t ch, float c) {
  w.u8(ch);
  w.u8(103);
  w.u16(uint16_t(int16_t(roundTo(c, 0.1f))));
}

void lppHumidity(Writer &w, uint8_t ch, float pct) {
  int32_t v = roundTo(pct, 0.5f);
  w.u8(ch);
  w.u8(104);
  w.u8(uint8_t(v < 0 ? 0 : v > 255 ? 255 : v));
}

void lppPressure(Writer &w, uint8_t ch, float hpa) {
  w.u8(ch);
  w.u8(115);
  w.u16(uint16_t(roundTo(hpa, 0.1f)));
}

void lppVoltage(Writer &w, uint8_t ch, float v) {
  w.u8(ch);
  w.u8(116);
  w.u16(uint16_t(roundTo(v < 0 ? 0 : v, 0.01f)));
}

void lppDigitalIn(Writer &w, uint8_t ch, uint8_t v) {
  w.u8(ch);
  w.u8(0);
  w.u8(v);
}

void lppDigitalOut(Writer &w, uint8_t ch, uint8_t v) {
  w.u8(ch);
  w.u8(1);
  w.u8(v);
}

void lppGeneric(Writer &w, uint8_t ch, uint32_t v) {
  w.u8(ch);
  w.u8(100);
  w.u32(v);
}

void lppPercentage(Writer &w, uint8_t ch, uint8_t v) {
  w.u8(ch);
  w.u8(120);
  w.u8(v > 100 ? 100 : v);
}

// ---------- self-description ----------

void Description::header(Writer &w, const Identity &id, const Profile &p, uint8_t part, uint8_t parts) {
  uplinkHeader(w, id, &p, true, true);
  w.u8(kDescribeFormat);
  w.u8(uint8_t((part & 0x0F) << 4 | (parts & 0x0F)));
}

Description::Description(Writer &w, const Identity &id, const Profile &p, uint8_t part, uint8_t parts) : w_(w) {
  header(w_, id, p, part, parts);
}

void Description::value(Format f, int32_t v) {
  switch (f) {
    case kU8:
    case kI8: w_.u8(uint8_t(v)); break;
    case kU16:
    case kI16: w_.u16(uint16_t(v)); break;
    case kU32:
    case kI32: w_.u32(uint32_t(v)); break;
    default: break;
  }
}

void Description::button(uint8_t code, const char *name) {
  w_.u8(code);
  w_.u8(0x00);
  w_.text(name, 24);
}

void Description::buttonWithValue(uint8_t code, Format f, int32_t v, const char *name) {
  w_.u8(code);
  w_.u8(uint8_t(0x00 | f));
  value(f, v);
  w_.text(name, 24);
}

void Description::toggle(uint8_t code, const char *name) {
  w_.u8(code);
  w_.u8(0x10 | kU8);
  w_.text(name, 24);
}

void Description::number(uint8_t code, Format f, int32_t min, int32_t max, int8_t scale, const char *name, const char *unit) {
  w_.u8(code);
  w_.u8(uint8_t(0x20 | f));
  value(f, min);
  value(f, max);
  w_.u8(uint8_t(scale));
  w_.text(name, 24);
  w_.text(unit ? unit : "", 8);
}

void Description::choiceBegin(uint8_t code, Format f, uint8_t count, const char *name) {
  w_.u8(code);
  w_.u8(uint8_t(0x30 | f));
  w_.u8(count);
  choice_format_ = f;
  choice_name_ = name;
  choice_left_ = count;
}

void Description::option(int32_t v, const char *name) {
  if (choice_left_ == 0) return;
  value(choice_format_, v);
  w_.text(name, 16);
  if (--choice_left_ == 0) w_.text(choice_name_, 24); // the entry's own name comes last
}

void Description::raw(uint8_t code, uint8_t max_len, const char *name) {
  w_.u8(code);
  w_.u8(0x40);
  w_.u8(max_len);
  w_.text(name, 24);
}

// ---------- downlink ----------

bool parseDownlink(const uint8_t *b, size_t n, const Identity &id, bool header, bool control, Downlink *out) {
  Downlink d;
  size_t pos = 0;
  uint16_t nid = id.node_id;
  uint32_t ser = id.serial;
  if (header) {
    if (n < 6) return false;
    nid = uint16_t(b[0] << 8 | b[1]);
    ser = uint32_t(b[2]) << 24 | uint32_t(b[3]) << 16 | uint32_t(b[4]) << 8 | b[5];
    if (nid != id.node_id && nid != kAnyNode) return false;  // another group
    if (ser != id.serial && ser != kAnySerial) return false; // another node
    if (nid == kAnyNode && ser != kAnySerial) return false;  // not a format the hub sends
    pos = 6;
    d.audience = nid == kAnyNode ? kEveryone : ser == kAnySerial ? kGroup : kThisNode;
  }
  if (control) {
    if (n < pos + 1) return false;
    uint8_t c = b[pos++];
    if (!(c & 0x80)) return false; // bit 7 is always set by the hub
    uint8_t aud = (c >> 5) & 0x03;
    if (aud > kEveryone) return false;
    if (header && aud != d.audience) return false; // the header and the control byte disagree
    d.audience = Audience(aud);
    d.has_control = true;
    d.confirm = (c & 0x10) != 0;
    d.seq = c & 0x0F;
  }
  d.data = b + pos;
  d.len = n - pos;
  if (out) *out = d;
  return true;
}

bool RepeatFilter::repeated(const Downlink &d, uint32_t now_ms, uint32_t window_ms) {
  if (!d.has_control) return false; // no sequence number to compare
  uint8_t i = d.audience;
  if (last_seq_[i] == d.seq && now_ms - last_ms_[i] < window_ms) return true;
  last_seq_[i] = d.seq;
  last_ms_[i] = now_ms;
  return false;
}

// ---------- airtime ----------

uint32_t airtimeUs(size_t len, uint8_t sf, uint16_t bw_khz, uint8_t cr, uint16_t preamble) {
  if (sf < 5 || sf > 12 || bw_khz == 0) return 0;
  uint32_t tsym_us = (uint32_t(1) << sf) * 1000u / bw_khz;
  int de = tsym_us > 16000 ? 1 : 0; // low data rate optimisation
  int crn = cr >= 5 && cr <= 8 ? cr - 4 : 1;
  int num = 8 * int(len) - 4 * sf + 28 + 16; // CRC on, explicit header
  int den = 4 * (sf - 2 * de);
  int extra = num > 0 ? (num + den - 1) / den * (crn + 4) : 0;
  uint32_t symbols_x4 = (uint32_t(preamble) * 4 + 17) + uint32_t(8 + extra) * 4; // preamble + 4.25
  return symbols_x4 * tsym_us / 4;
}

} // namespace lorahub
