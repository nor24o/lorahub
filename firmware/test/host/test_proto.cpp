// Tests of the protocol code on a PC, against the examples in docs/protocol.md.
//   g++ -std=c++11 -Wall -Wextra -I../../lib/lorahub_proto test_proto.cpp ../../lib/lorahub_proto/*.cpp -o test_proto && ./test_proto
// With -v it also prints the packets of every node type (used to check them
// against LoRaHub itself).
#include <stdio.h>
#include <string.h>

#include "kit.h"
#include "lorahub_proto.h"

using namespace lorahub;

static int failures = 0;

#define CHECK(cond)                                                     \
  do {                                                                  \
    if (!(cond)) {                                                      \
      printf("FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond);            \
      failures++;                                                       \
    }                                                                   \
  } while (0)

static void hex(const uint8_t *b, size_t n, char *out) {
  for (size_t i = 0; i < n; i++) sprintf(out + 2 * i, "%02X", b[i]);
  out[2 * n] = 0;
}

static size_t unhex(const char *s, uint8_t *out) {
  size_t n = 0;
  for (; s[0] && s[1]; s += 2) {
    unsigned v;
    sscanf(s, "%2x", &v);
    out[n++] = uint8_t(v);
  }
  return n;
}

static bool sameHex(const Writer &w, const char *want) {
  char got[600];
  hex(w.data(), w.size(), got);
  if (strcmp(got, want) != 0) {
    printf("  got  %s\n  want %s\n", got, want);
    return false;
  }
  return true;
}

static const Identity kNode = {0xCAFE, 0x96B840EB};

static void testDescriptionExample() {
  // The example in docs/protocol.md: Relay, Blink, Interval; 43 bytes.
  uint8_t buf[64];
  Writer w(buf, sizeof buf);
  Profile p;
  p.cls = 'C';
  Description d(w, kNode, p);
  d.toggle(0x01, "Relay");
  d.button(0x02, "Blink");
  d.number(0x03, kU16, 10, 3600, 0, "Interval", "s");
  CHECK(d.ok());
  CHECK(w.size() == 43);
  CHECK(sameHex(w, "CAFE96B840EB88D11101110552656C6179020005426C696E6B0323000A0E100008496E74657276616C0173"));
}

static void testProfileAndRadioBlock() {
  // docs: CAFE 96B840EB 90 84ADD2 90 0167011002 (class C, radio block 869.525 MHz SF9).
  uint8_t buf[32];
  Writer w(buf, sizeof buf);
  Profile p;
  p.cls = 'C';
  p.radio_block = true;
  p.rx_freq_hz = 869525000;
  p.rx_sf = 9;
  uplinkHeader(w, kNode, &p, true);
  lppTemperature(w, 1, 27.2f);
  w.u8(0x02); // the example's last byte
  CHECK(sameHex(w, "CAFE96B840EB9084ADD2900167011002"));

  Profile a; // class A, nothing else
  CHECK(profileByte(a, false) == 0x00);
  a.invert_iq = true;
  a.user = 0x05;
  CHECK(profileByte(a, true) == 0x2D);
}

static void testLPP() {
  uint8_t buf[64];
  Writer w(buf, sizeof buf);
  lppTemperature(w, 1, -5.25f); // rounds to -5.3 = 0xFFCB
  lppHumidity(w, 2, 55.0f);
  lppPressure(w, 3, 1013.25f); // rounds to 1013.3
  lppVoltage(w, 4, 3.71f);
  lppDigitalOut(w, 5, 1);
  lppGeneric(w, 7, 123456);
  CHECK(sameHex(w, "0167FFCB02686E03732795047401730501010764" "0001E240"));
}

static void testDownlinks() {
  uint8_t b[32];
  Downlink d;
  size_t n = unhex("CAFE96B840EB840101", b); // relay on, control byte, seq 4
  CHECK(parseDownlink(b, n, kNode, true, true, &d));
  CHECK(d.audience == kThisNode && d.has_control && !d.confirm && d.seq == 4);
  CHECK(d.len == 2 && d.data[0] == 0x01 && d.data[1] == 0x01);

  n = unhex("CAFE96B840EB9203012C", b); // confirm asked, interval 300 s
  CHECK(parseDownlink(b, n, kNode, true, true, &d));
  CHECK(d.confirm && d.seq == 2 && d.len == 3 && d.data[0] == 0x03);

  n = unhex("CAFEFFFFFFFFA0FF", b); // group broadcast: describe
  CHECK(parseDownlink(b, n, kNode, true, true, &d));
  CHECK(d.audience == kGroup && d.len == 1 && d.data[0] == kDescribeRequest);

  n = unhex("FFFFFFFFFFFFC0FF", b); // everyone
  CHECK(parseDownlink(b, n, kNode, true, true, &d));
  CHECK(d.audience == kEveryone);

  n = unhex("CAFE96B840EB0101", b); // header, no control byte
  CHECK(parseDownlink(b, n, kNode, true, false, &d));
  CHECK(!d.has_control && d.len == 2);

  n = unhex("0101", b); // no header at all
  CHECK(parseDownlink(b, n, kNode, false, false, &d));
  CHECK(d.len == 2 && d.data[1] == 0x01);

  // Not for this node.
  n = unhex("CAFE96B840EC840101", b); // another serial
  CHECK(!parseDownlink(b, n, kNode, true, true, &d));
  n = unhex("CAFF96B840EB840101", b); // another group
  CHECK(!parseDownlink(b, n, kNode, true, true, &d));
  n = unhex("CAFE000000020167011005", b); // another node's uplink
  CHECK(!parseDownlink(b, n, kNode, true, true, &d));
  n = unhex("CAFE96B840EB0167011005", b); // bit 7 clear: not from the hub
  CHECK(!parseDownlink(b, n, kNode, true, true, &d));
  n = unhex("CAFE96B840EBA00101", b); // control says group, header says this node
  CHECK(!parseDownlink(b, n, kNode, true, true, &d));
  n = unhex("FFFF96B840EBC00101", b); // FFFF with a real serial
  CHECK(!parseDownlink(b, n, kNode, true, true, &d));
  n = unhex("CAFE96B8", b); // too short
  CHECK(!parseDownlink(b, n, kNode, true, true, &d));
}

static void testRepeats() {
  uint8_t b[16];
  Downlink d;
  RepeatFilter f;
  size_t n = unhex("CAFEFFFFFFFFA5FF", b);
  parseDownlink(b, n, kNode, true, true, &d);
  CHECK(!f.repeated(d, 1000));
  CHECK(f.repeated(d, 1500));   // the same broadcast through the second gateway
  CHECK(!f.repeated(d, 40000)); // much later: a new message with the same number
  n = unhex("CAFE96B840EB850101", b);
  parseDownlink(b, n, kNode, true, true, &d);
  CHECK(!f.repeated(d, 41000)); // direct messages count separately
}

static void testAirtime() {
  CHECK(airtimeUs(10, 7, 125, 5, 8) == 41216);      // 41.2 ms
  uint32_t t = airtimeUs(43, 12, 125, 5, 8);         // docs: about 2.1 s
  CHECK(t > 2100000 && t < 2200000);
  uint32_t t9 = airtimeUs(43, 9, 125, 5, 8);         // docs: about 0.3 s
  CHECK(t9 > 250000 && t9 < 350000);
}

static void printPackets(bool verbose) {
  // Every node type's description and a typical report: checked against
  // LoRaHub's own parser and the kit decoder.
  struct T {
    const char *name;
    kit::Type type;
    char cls;
    uint8_t sf;
  } types[] = {{"sensor", kit::kSensor, 'A', 9}, {"relay", kit::kRelay, 'C', 9}, {"button", kit::kButton, 'A', 9},
               {"counter", kit::kCounter, 'A', 9}, {"relay_sf12", kit::kRelay, 'C', 12}};
  for (const T &t : types) {
    Profile p;
    p.cls = t.cls;
    if (t.cls == 'C') {
      p.radio_block = true;
      p.rx_freq_hz = 869525000;
      p.rx_sf = 9;
    }
    kit::Features f;
    f.type = t.type;
    f.led = true;
    uint8_t out[4][kit::kMaxDescribePacket];
    size_t lens[4];
    size_t parts = kit::describe(kNode, p, f, kit::maxPacket(t.sf), out, lens, 4);
    CHECK(parts >= 1);
    for (size_t i = 0; i < parts; i++) {
      CHECK(lens[i] <= kit::maxPacket(t.sf));
      char h[300];
      hex(out[i], lens[i], h);
      if (verbose) printf("describe %s %zu/%zu %s\n", t.name, i + 1, parts, h);
    }
    if (t.type == kit::kRelay && t.sf == 9) CHECK(parts == 1);
    if (t.type == kit::kRelay && t.sf == 12) CHECK(parts == 3); // 51 bytes each at SF12

    uint8_t buf[64];
    Writer w(buf, sizeof buf);
    uplinkHeader(w, kNode, &p, true);
    kit::Readings r;
    r.has_battery = true;
    r.battery_v = 3.92f;
    r.has_interval = true;
    r.interval_s = 300;
    if (t.type == kit::kSensor) {
      r.has_temperature = r.has_humidity = r.has_pressure = true;
      r.temperature = 21.5f;
      r.humidity = 48.5f;
      r.pressure = 1008.4f;
    }
    if (t.type == kit::kRelay) r.has_relay = r.relay = true;
    if (t.type == kit::kButton) r.has_presses = r.pressed = true, r.presses = 3;
    if (t.type == kit::kCounter) r.has_count = true, r.count = 1234;
    kit::writeReadings(w, r);
    CHECK(w.ok());
    char h[200];
    hex(w.data(), w.size(), h);
    if (verbose) printf("report %s %s\n", t.name, h);
  }
}

int main(int argc, char **argv) {
  bool verbose = argc > 1 && strcmp(argv[1], "-v") == 0;
  testDescriptionExample();
  testProfileAndRadioBlock();
  testLPP();
  testDownlinks();
  testRepeats();
  testAirtime();
  printPackets(verbose);
  if (failures) {
    printf("%d check(s) failed\n", failures);
    return 1;
  }
  printf("all checks passed\n");
  return 0;
}
