#include "kit.h"

#include <string.h>

namespace kit {

using namespace lorahub;

void writeReadings(Writer &w, const Readings &r) {
  if (r.has_temperature) lppTemperature(w, kChTemperature, r.temperature);
  if (r.has_humidity) lppHumidity(w, kChHumidity, r.humidity);
  if (r.has_pressure) lppPressure(w, kChPressure, r.pressure);
  if (r.has_battery) lppVoltage(w, kChBattery, r.battery_v);
  if (r.has_relay) lppDigitalOut(w, kChRelay, r.relay ? 1 : 0);
  if (r.has_presses) lppGeneric(w, kChPresses, r.presses);
  if (r.has_count) lppGeneric(w, kChCount, r.count);
  if (r.has_interval) lppGeneric(w, kChInterval, r.interval_s);
  if (r.pressed) lppDigitalIn(w, kChButton, 1);
}

size_t maxPacket(uint8_t sf) {
  if (sf >= 10) return 51;
  if (sf == 9) return 115;
  return 222;
}

// The entries of a node type, one at a time: entry(i, w) writes entry i and
// returns false when there is no entry i.
static bool entry(const Features &f, int i, Writer &w) {
  Description d(w);
  int n = 0;
  if (f.type == kRelay) {
    if (i == n++) { d.toggle(kCmdRelay, "Relay"); return true; }
    if (i == n++) { d.number(kCmdPulse, kU16, 1, f.pulse_max_s, 0, "Pulse", "s"); return true; }
  }
  if (f.type == kCounter) {
    if (i == n++) { d.button(kCmdResetCount, "Reset count"); return true; }
  }
  if (i == n++) { d.number(kCmdInterval, kU16, f.interval_min_s, f.interval_max_s, 0, "Interval", "s"); return true; }
  if (f.led) {
    if (i == n++) { d.button(kCmdIdentify, "Identify"); return true; }
  }
  if (i == n++) { d.button(kCmdRestart, "Restart"); return true; }
  return false;
}

size_t describe(const Identity &id, const Profile &p, const Features &f, size_t max_packet,
                uint8_t out[][kMaxDescribePacket], size_t lens[], size_t max_parts) {
  if (max_packet > kMaxDescribePacket) max_packet = kMaxDescribePacket;
  uint8_t head[16];
  Writer hw(head, sizeof head);
  Description::header(hw, id, p, 1, 1);
  const size_t head_len = hw.size();

  // First pass: how many parts are needed.
  size_t parts = 0, used = max_packet; // "used" past the limit forces a new part
  for (int i = 0;; i++) {
    uint8_t e[64];
    Writer ew(e, sizeof e);
    if (!entry(f, i, ew)) break;
    if (!ew.ok() || head_len + ew.size() > max_packet) return 0; // one entry is too long
    if (used + ew.size() > max_packet) {
      parts++;
      used = head_len;
    }
    used += ew.size();
  }
  if (parts == 0 || parts > max_parts || parts > 15) return 0;

  // Second pass: write them, whole entries only in each part.
  size_t part = 0;
  Writer cur(out[0], max_packet);
  for (int i = 0;; i++) {
    uint8_t e[64];
    Writer ew(e, sizeof e);
    if (!entry(f, i, ew)) break;
    if (part == 0 || cur.size() + ew.size() > max_packet) {
      if (part > 0) lens[part - 1] = cur.size();
      cur = Writer(out[part], max_packet);
      part++;
      Description::header(cur, id, p, uint8_t(part), uint8_t(parts));
    }
    cur.bytes(ew.data(), ew.size());
  }
  lens[part - 1] = cur.size();
  return parts;
}

} // namespace kit
