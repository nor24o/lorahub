// LoRaHub node kit: one firmware for every node type. What a node is (type,
// identity, radio, pins, sensors) is chosen at build time: see
// include/node_config.h for every setting, and platformio.ini and nodes/ for
// ready builds.
//
// Class A types (sensor, button, counter) wake up, send a report, listen in
// the receive window 1 s later, and go back to deep sleep. The relay type
// (class C) listens all the time on its own frequency and reports its state.
#include <Arduino.h>
#include <Preferences.h>
#include <RadioLib.h>
#include <SPI.h>
#include <sys/time.h>

#include "driver/rtc_io.h"
#include "esp_sleep.h"
#include "kit.h"
#include "lorahub_proto.h"
#include "node_config.h"

#if SENSOR_DS18B20
#include <DallasTemperature.h>
#include <OneWire.h>
#endif
#if SENSOR_BME280
#include <Adafruit_BME280.h>
#include <Wire.h>
#endif

#ifndef REPORT_BATTERY // mains-powered relays have no battery to report
#define REPORT_BATTERY (BATTERY_PIN >= 0 && NODE_TYPE != NODE_RELAY)
#endif

#if DEBUG_SERIAL
#define LOG(...) Serial.printf(__VA_ARGS__)
#else
#define LOG(...) \
  do {           \
  } while (0)
#endif

static_assert(NODE_CLASS != 'C' || RX_FREQ_MHZ > 0, "A class C node needs a fixed RX_FREQ_MHZ");

using namespace lorahub;

// ---------- radio ----------

#if RADIO_SX1262
static Module radioModule(LORA_NSS, LORA_IRQ, LORA_RST, LORA_BUSY, SPI);
static SX1262 radio(&radioModule);
static const char *kChip = "SX1262";
#else
static Module radioModule(LORA_NSS, LORA_IRQ, LORA_RST, LORA_DIO1, SPI);
static SX1276 radio(&radioModule);
static const char *kChip = "SX1276";
#endif

// The 8 channels the LoRaHub gateways listen on (EU868, 125 kHz, SF7-SF12).
static const float kChannels[] = {868.1, 868.3, 868.5, 867.1, 867.3, 867.5, 867.7, 867.9};

static constexpr bool kRadioBlock = RX_FREQ_MHZ > 0 || RX_SF > 0;
static constexpr uint8_t kRxSF = RX_SF > 0 ? RX_SF : UPLINK_SF;
static constexpr uint16_t kRxBW = kRadioBlock ? RX_BW_KHZ : UPLINK_BW_KHZ;

// ---------- state ----------

// Kept in RTC memory through deep sleep; cleared at power-on. Not every node
// type uses all of them.
#define MAYBE_UNUSED __attribute__((unused))
RTC_DATA_ATTR static uint8_t rtcHop MAYBE_UNUSED = 0;
RTC_DATA_ATTR static bool rtcDescribed = false;
RTC_DATA_ATTR static uint64_t rtcNextReportUs MAYBE_UNUSED = 0;
RTC_DATA_ATTR static uint32_t rtcPresses MAYBE_UNUSED = 0;
RTC_DATA_ATTR static uint32_t rtcCount = 0;
RTC_DATA_ATTR static bool rtcCountLoaded = false;
RTC_DATA_ATTR static uint64_t rtcBudgetStartUs = 0;
RTC_DATA_ATTR static uint32_t rtcBudgetUsedUs = 0;

static Identity id;
static Profile profile;
static kit::Features features;
static Preferences prefs; // kept in flash: interval, relay state, pulse count
static uint16_t intervalS = REPORT_INTERVAL_S;
static float lastUpFreq = UPLINK_FREQ_MHZ;
static uint32_t txDoneMs = 0;
static volatile bool rxFlag = false;
static bool radioUp = false; // the radio was started in this wake

#if NODE_CLASS == 'C'
static RepeatFilter repeats;
static uint32_t lastReportMs = 0;
static uint32_t identifyUntilMs = 0;
#endif
#if NODE_TYPE == NODE_RELAY
static bool relayOn = false;
static uint32_t pulseUntilMs = 0;
#endif

static void IRAM_ATTR onPacket() { rxFlag = true; }

// Microseconds since power-on; keeps counting through deep sleep.
static uint64_t nowUs() {
  struct timeval tv;
  gettimeofday(&tv, nullptr);
  return uint64_t(tv.tv_sec) * 1000000ULL + uint64_t(tv.tv_usec);
}

static void logHex(const char *what, const uint8_t *b, size_t n) {
#if DEBUG_SERIAL
  Serial.printf("%s, %u bytes: ", what, unsigned(n));
  for (size_t i = 0; i < n; i++) Serial.printf("%02X", b[i]);
  Serial.println();
#endif
}

// SERIAL_ID from the last four bytes of the chip's factory MAC address.
static uint32_t serialFromChip() {
  uint64_t mac = ESP.getEfuseMac();
  uint8_t m[6];
  for (int i = 0; i < 6; i++) m[i] = uint8_t(mac >> (8 * i));
  uint32_t s = uint32_t(m[2]) << 24 | uint32_t(m[3]) << 16 | uint32_t(m[4]) << 8 | m[5];
  if (s == 0 || s == kAnySerial) s ^= 0x5A5A5A5A;
  return s;
}

// The shortest interval the duty cycle allows for a typical report.
static uint16_t minIntervalS() {
  uint32_t toa = airtimeUs(32, UPLINK_SF, UPLINK_BW_KHZ, UPLINK_CR, LORA_PREAMBLE);
  uint32_t s = uint32_t(uint64_t(toa) * 100 / DUTY_CYCLE_PCT / 1000000ULL) + 1;
  return s > INTERVAL_MIN_S ? uint16_t(s) : uint16_t(INTERVAL_MIN_S);
}

static uint16_t clampInterval(uint32_t s) {
  if (s < features.interval_min_s) return features.interval_min_s;
  if (s > features.interval_max_s) return features.interval_max_s;
  return uint16_t(s);
}

// ---------- radio settings ----------

static int beginRadio() {
#if RADIO_SX1262
  int st = radio.begin(UPLINK_FREQ_MHZ, UPLINK_BW_KHZ, UPLINK_SF, UPLINK_CR, LORA_SYNC_WORD, TX_POWER_DBM, LORA_PREAMBLE, TCXO_VOLTAGE, false);
  if (st == RADIOLIB_ERR_NONE && DIO2_RF_SWITCH) st = radio.setDio2AsRfSwitch(true);
#else
  int st = radio.begin(UPLINK_FREQ_MHZ, UPLINK_BW_KHZ, UPLINK_SF, UPLINK_CR, LORA_SYNC_WORD, TX_POWER_DBM, LORA_PREAMBLE, 0);
#endif
  return st;
}

static void useUplink() {
  float f = UPLINK_FREQ_MHZ;
#if UPLINK_HOP
  f = kChannels[rtcHop++ % 8];
#endif
  lastUpFreq = f;
  radio.standby();
  radio.setFrequency(f);
  radio.setBandwidth(UPLINK_BW_KHZ);
  radio.setSpreadingFactor(UPLINK_SF);
  radio.setCodingRate(UPLINK_CR);
  radio.invertIQ(false); // uplinks always use normal IQ
}

// Where downlinks come: the radio block's settings, else the uplink's.
static void useDownlink() {
  radio.standby();
  radio.setFrequency(RX_FREQ_MHZ > 0 ? float(RX_FREQ_MHZ) : lastUpFreq);
  radio.setBandwidth(kRxBW);
  radio.setSpreadingFactor(kRxSF);
  radio.setCodingRate(RX_CR); // the receiver reads the coding rate from the packet header anyway
  radio.invertIQ(RX_INVERT_IQ);
}

// The duty cycle: at most DUTY_CYCLE_PCT of each hour on air.
static bool budgetAllows(uint32_t toa) {
  uint64_t now = nowUs();
  if (rtcBudgetStartUs == 0 || now - rtcBudgetStartUs >= 3600000000ULL) {
    rtcBudgetStartUs = now;
    rtcBudgetUsedUs = 0;
  }
  return uint64_t(rtcBudgetUsedUs) + toa <= 3600000000ULL * DUTY_CYCLE_PCT / 100;
}

static bool transmitPacket(uint8_t *b, size_t n, const char *what) {
  uint32_t toa = airtimeUs(n, UPLINK_SF, UPLINK_BW_KHZ, UPLINK_CR, LORA_PREAMBLE);
  if (!budgetAllows(toa)) {
    LOG("not sending the %s: the hour's airtime is used up (duty cycle)\n", what);
    return false;
  }
  useUplink();
  int st = radio.transmit(b, n);
  txDoneMs = millis();
  rtcBudgetUsedUs += toa;
  if (st != RADIOLIB_ERR_NONE) {
    LOG("sending the %s failed: RadioLib error %d\n", what, st);
    return false;
  }
  LOG("sent the %s on %.1f MHz SF%d, %lu ms on air\n", what, lastUpFreq, UPLINK_SF, (unsigned long)(toa / 1000));
  logHex("  ", b, n);
  return true;
}

// ---------- the node's parts ----------

#if NODE_TYPE == NODE_RELAY
static void setRelay(bool on) {
  bool high = RELAY_ACTIVE_HIGH ? on : !on;
  digitalWrite(RELAY_PIN, high ? HIGH : LOW);
  if (on != relayOn && RELAY_RESTORE) prefs.putBool("relay", on);
  relayOn = on;
  LOG("relay %s\n", on ? "on" : "off");
}
#endif

static void identify() {
#if LED_PIN >= 0
#if NODE_CLASS == 'C'
  identifyUntilMs = millis() + 5000; // blinks in loop()
#else
  for (int i = 0; i < 20; i++) {
    digitalWrite(LED_PIN, i % 2 ? LOW : HIGH);
    delay(150);
  }
  digitalWrite(LED_PIN, LOW);
#endif
#endif
}

static bool readBattery(float *volts) {
#if BATTERY_PIN >= 0
#if BATTERY_ENABLE_PIN >= 0
  pinMode(BATTERY_ENABLE_PIN, OUTPUT);
  digitalWrite(BATTERY_ENABLE_PIN, BATTERY_ENABLE_LEVEL ? HIGH : LOW);
  delay(10);
#endif
  uint32_t mv = 0;
  for (int i = 0; i < 8; i++) mv += analogReadMilliVolts(BATTERY_PIN);
#if BATTERY_ENABLE_PIN >= 0
  pinMode(BATTERY_ENABLE_PIN, INPUT);
#endif
  *volts = float(mv) / 8.0f * float(BATTERY_DIVIDER) / 1000.0f;
  return true;
#else
  (void)volts;
  return false;
#endif
}

static void readSensors(kit::Readings *r) {
#if SENSOR_DS18B20
  static OneWire oneWire(ONEWIRE_PIN);
  static DallasTemperature ds(&oneWire);
  ds.begin();
  ds.requestTemperatures();
  float t = ds.getTempCByIndex(0);
  if (t > -100 && t < 150) {
    r->has_temperature = true;
    r->temperature = t;
  } else {
    LOG("no DS18B20 found on pin %d\n", ONEWIRE_PIN);
  }
#endif
#if SENSOR_BME280
  static Adafruit_BME280 bme;
  Wire.begin(I2C_SDA, I2C_SCL);
  if (bme.begin(BME280_ADDRESS, &Wire)) {
    bme.setSampling(Adafruit_BME280::MODE_FORCED, Adafruit_BME280::SAMPLING_X1, Adafruit_BME280::SAMPLING_X1,
                    Adafruit_BME280::SAMPLING_X1, Adafruit_BME280::FILTER_OFF);
    bme.takeForcedMeasurement();
    r->has_temperature = r->has_humidity = r->has_pressure = true;
    r->temperature = bme.readTemperature();
    r->humidity = bme.readHumidity();
    r->pressure = bme.readPressure() / 100.0f;
  } else {
    LOG("no BME280 found at 0x%02X (SDA %d, SCL %d)\n", BME280_ADDRESS, I2C_SDA, I2C_SCL);
  }
#endif
  if (REPORT_BATTERY) r->has_battery = readBattery(&r->battery_v);
#if NODE_TYPE == NODE_RELAY
  r->has_relay = true;
  r->relay = relayOn;
#endif
#if NODE_TYPE == NODE_BUTTON
  r->has_presses = true;
  r->presses = rtcPresses;
#endif
#if NODE_TYPE == NODE_COUNTER
  r->has_count = true;
  r->count = rtcCount;
#endif
  r->has_interval = true;
  r->interval_s = intervalS;
}

// ---------- packets ----------

static bool sendReport(bool pressed) {
  uint8_t buf[64];
  Writer w(buf, sizeof buf);
  uplinkHeader(w, id, &profile, SEND_PROFILE);
  kit::Readings r;
  readSensors(&r);
  r.pressed = pressed;
  kit::writeReadings(w, r);
  if (!w.ok()) return false;
  bool ok = transmitPacket(buf, w.size(), "report");
#if NODE_CLASS == 'C'
  lastReportMs = millis();
#endif
#if NODE_TYPE == NODE_COUNTER
  if (ok && prefs.getULong("count", 0) != rtcCount) prefs.putULong("count", rtcCount);
#endif
  return ok;
}

static void sendDescription() {
  static uint8_t parts[4][kit::kMaxDescribePacket];
  size_t lens[4];
  size_t n = kit::describe(id, profile, features, kit::maxPacket(UPLINK_SF), parts, lens, 4);
  if (n == 0) {
    LOG("the description does not fit in packets at SF%d\n", UPLINK_SF);
    return;
  }
  for (size_t i = 0; i < n; i++) {
    if (i > 0) delay(500);
    if (!transmitPacket(parts[i], lens[i], "description")) return;
  }
  rtcDescribed = true;
}

// What to do after a downlink.
struct After {
  bool report = false;
  bool describe = false;
  bool restart = false;
};

static void handlePacket(const uint8_t *b, size_t n, After *a) {
  logHex("heard", b, n);
  Downlink d;
  if (!parseDownlink(b, n, id, DOWNLINK_HEADER, DOWNLINK_CONTROL, &d)) {
    LOG("  not for this node\n");
    return;
  }
#if NODE_CLASS == 'C'
  if (repeats.repeated(d, millis())) {
    LOG("  already done (the same message through another gateway)\n");
    return;
  }
#endif
  static const char *kAudience[] = {"this node", "the group", "everyone"};
  LOG("  for %s, sequence %u%s\n", kAudience[d.audience], d.seq, d.confirm ? ", confirmation asked" : "");
  if (d.confirm) a->report = true;
  if (d.len == 0) return;
  const uint8_t *c = d.data;
  switch (c[0]) {
    case kDescribeRequest:
      a->describe = true;
      break;
    case kit::kCmdInterval:
      if (d.len >= 3) {
        intervalS = clampInterval(uint32_t(c[1]) << 8 | c[2]);
        prefs.putUShort("interval", intervalS);
        LOG("  interval %u s\n", intervalS);
        a->report = true;
      }
      break;
#if NODE_TYPE == NODE_RELAY
    case kit::kCmdRelay:
      if (d.len >= 2) {
        pulseUntilMs = 0;
        setRelay(c[1] != 0);
        a->report = true;
      }
      break;
    case kit::kCmdPulse:
      if (d.len >= 3) {
        uint32_t s = uint32_t(c[1]) << 8 | c[2];
        if (s < 1) s = 1;
        if (s > PULSE_MAX_S) s = PULSE_MAX_S;
        setRelay(true);
        pulseUntilMs = (millis() + s * 1000UL) | 1; // never 0, which means "no pulse"
        LOG("  off again in %lu s\n", (unsigned long)s);
        a->report = true;
      }
      break;
#endif
#if NODE_TYPE == NODE_COUNTER
    case kit::kCmdResetCount:
      rtcCount = 0;
      prefs.putULong("count", 0);
      a->report = true;
      break;
#endif
    case kit::kCmdIdentify:
      identify();
      break;
    case kit::kCmdRestart:
      a->restart = true;
      break;
    default:
      LOG("  unknown command %02X\n", c[0]);
  }
}

// ---------- class A: report, receive window, sleep ----------

#if NODE_CLASS != 'C'
// Listens in the receive window after the last uplink. Returns the length of
// the packet heard, 0 for none.
static size_t receiveWindow(uint8_t *buf, size_t cap) {
  useDownlink();
  int32_t wait = int32_t(RX_DELAY_MS) - int32_t(RX_MARGIN_MS) - int32_t(millis() - txDoneMs);
  if (wait > 0) delay(uint32_t(wait));
  // Long enough for the longest downlink to start inside the window and end.
  uint32_t window = 2 * RX_MARGIN_MS + airtimeUs(MAX_DOWNLINK + 7, kRxSF, kRxBW, 5, LORA_PREAMBLE) / 1000;
  rxFlag = false;
  radio.setPacketReceivedAction(onPacket);
  radio.startReceive();
  uint32_t t0 = millis();
  size_t got = 0;
  while (millis() - t0 < window) {
    if (!rxFlag) {
      delay(1);
      continue;
    }
    rxFlag = false;
    size_t n = radio.getPacketLength();
    if (n > cap) n = cap;
    int st = radio.readData(buf, n);
    if (st == RADIOLIB_ERR_NONE && n > 0) {
      got = n;
      break;
    }
    radio.startReceive(); // a damaged packet: keep listening
  }
  radio.clearPacketReceivedAction();
  radio.standby();
  return got;
}

static void classA(bool pressed) {
  if (DESCRIBE_ON_BOOT && !rtcDescribed) {
    sendDescription();
    delay(500);
  }
  // A report, then its receive window. A command there gets a fresh report
  // (so LoRaHub sees the new state) and with it another window, for the next
  // command waiting in the queue.
  for (int round = 0; round < 4; round++) {
    if (!sendReport(pressed && round == 0)) return;
    uint8_t rx[MAX_DOWNLINK + 16];
    size_t n = receiveWindow(rx, sizeof rx);
    if (n == 0) {
      LOG("nothing in the receive window\n");
      return;
    }
    After a;
    handlePacket(rx, n, &a);
    if (a.restart) {
      LOG("restarting\n");
      delay(100);
      ESP.restart();
    }
    if (a.describe) {
      delay(300);
      sendDescription();
      delay(300);
    } else if (!a.report) {
      return;
    }
  }
}

#if NODE_TYPE == NODE_BUTTON || NODE_TYPE == NODE_COUNTER
// Waits until the button or pulse input is let go (3 s at most).
static void waitRelease(int pin, bool activeLow) {
  rtc_gpio_deinit(gpio_num_t(pin));
  pinMode(pin, activeLow ? INPUT_PULLUP : INPUT_PULLDOWN);
  int active = activeLow ? LOW : HIGH;
  uint32_t t0 = millis(), quiet = millis();
  while (millis() - t0 < 3000) {
    if (digitalRead(pin) == active) quiet = millis();
    if (millis() - quiet > 50) return;
    delay(5);
  }
}
#endif

static void sleepNow() {
  if (radioUp) radio.sleep(); // otherwise it is still asleep from last time
  uint64_t now = nowUs();
  uint64_t wait = rtcNextReportUs > now ? rtcNextReportUs - now : 1000000ULL;
  esp_sleep_enable_timer_wakeup(wait);
#if NODE_TYPE == NODE_BUTTON || NODE_TYPE == NODE_COUNTER
  const int pin = NODE_TYPE == NODE_BUTTON ? BUTTON_PIN : PULSE_PIN;
  const bool low = NODE_TYPE == NODE_BUTTON ? BUTTON_ACTIVE_LOW : PULSE_ACTIVE_LOW;
  pinMode(pin, low ? INPUT_PULLUP : INPUT_PULLDOWN);
  if (digitalRead(pin) != (low ? LOW : HIGH)) { // still held: wake by the timer only, or it wakes at once
    if (low) {
      rtc_gpio_pullup_en(gpio_num_t(pin));
      rtc_gpio_pulldown_dis(gpio_num_t(pin));
    } else {
      rtc_gpio_pulldown_en(gpio_num_t(pin));
      rtc_gpio_pullup_dis(gpio_num_t(pin));
    }
    esp_sleep_enable_ext0_wakeup(gpio_num_t(pin), low ? 0 : 1);
  }
#endif
  LOG("sleeping %lu s\n", (unsigned long)(wait / 1000000ULL));
#if DEBUG_SERIAL
  Serial.flush();
#endif
  esp_deep_sleep_start();
}
#endif

// ---------- start ----------

static const char *typeName() {
  switch (NODE_TYPE) {
    case NODE_RELAY: return "relay";
    case NODE_BUTTON: return "button";
    case NODE_COUNTER: return "counter";
    default: return "sensor";
  }
}

void setup() {
#if DEBUG_SERIAL
  Serial.begin(115200);
  delay(NODE_CLASS == 'C' ? 1000 : 20);
#endif
  esp_sleep_wakeup_cause_t cause = esp_sleep_get_wakeup_cause();
  bool powerOn = cause == ESP_SLEEP_WAKEUP_UNDEFINED;

  id.node_id = NODE_ID;
  id.serial = NODE_SERIAL ? uint32_t(NODE_SERIAL) : serialFromChip();
  profile.cls = NODE_CLASS;
  profile.invert_iq = RX_INVERT_IQ;
  profile.radio_block = kRadioBlock;
  profile.rx_freq_hz = uint32_t(RX_FREQ_MHZ * 1e6 + 0.5);
  profile.rx_sf = RX_SF;
  profile.rx_bw_khz = RX_BW_KHZ;
  profile.rx_cr = RX_CR;
  features.type = kit::Type(NODE_TYPE);
  features.led = LED_PIN >= 0;
  features.interval_min_s = minIntervalS();
  features.interval_max_s = INTERVAL_MAX_S;
  features.pulse_max_s = PULSE_MAX_S;

  prefs.begin("lorahub", false);
  intervalS = clampInterval(prefs.getUShort("interval", REPORT_INTERVAL_S));
  if (!rtcCountLoaded) {
    rtcCount = prefs.getULong("count", 0);
    rtcCountLoaded = true;
  }
  if (powerOn) {
    LOG("\nLoRaHub node kit: %s node, class %c, %s\n", typeName(), NODE_CLASS, kChip);
    LOG("NODE_ID %04X  SERIAL_ID %08lX  (in LoRaHub: group %04X, node %08lX)\n", id.node_id, (unsigned long)id.serial,
        id.node_id, (unsigned long)id.serial);
    LOG("uplinks %.1f MHz%s SF%d %d kHz, %d dBm; listens on ", float(UPLINK_FREQ_MHZ), UPLINK_HOP ? " (hopping)" : "",
        UPLINK_SF, UPLINK_BW_KHZ, TX_POWER_DBM);
    if (RX_FREQ_MHZ > 0) LOG("%.3f MHz SF%d\n", float(RX_FREQ_MHZ), kRxSF);
    else LOG("the uplink's channel, SF%d\n", kRxSF);
    LOG("report every %u s\n", intervalS);
  }

#if LED_PIN >= 0
  pinMode(LED_PIN, OUTPUT);
  digitalWrite(LED_PIN, LOW);
#endif
#if NODE_TYPE == NODE_RELAY
  bool start = RELAY_RESTORE ? prefs.getBool("relay", false) : false;
  digitalWrite(RELAY_PIN, (RELAY_ACTIVE_HIGH ? start : !start) ? HIGH : LOW);
  pinMode(RELAY_PIN, OUTPUT);
  relayOn = start;
#if BUTTON_PIN >= 0
  pinMode(BUTTON_PIN, BUTTON_ACTIVE_LOW ? INPUT_PULLUP : INPUT_PULLDOWN);
#endif
#endif

  bool pressed MAYBE_UNUSED = false;
#if NODE_TYPE == NODE_BUTTON
  if (cause == ESP_SLEEP_WAKEUP_EXT0) {
    rtcPresses++;
    pressed = true;
    LOG("button pressed (%lu since power-on)\n", (unsigned long)rtcPresses);
    waitRelease(BUTTON_PIN, BUTTON_ACTIVE_LOW);
  }
#endif

#if NODE_TYPE == NODE_COUNTER
  if (cause == ESP_SLEEP_WAKEUP_EXT0) {
    rtcCount++;
    waitRelease(PULSE_PIN, PULSE_ACTIVE_LOW);
    if (nowUs() < rtcNextReportUs) sleepNow(); // counted; not time to report yet
  }
#endif

  SPI.begin(LORA_SCK, LORA_MISO, LORA_MOSI, LORA_NSS);
  int st = beginRadio();
  radioUp = st == RADIOLIB_ERR_NONE;
  if (!radioUp) {
    LOG("the radio did not start: RadioLib error %d (check the pins and the board)\n", st);
#if NODE_CLASS == 'C'
    delay(10000);
    ESP.restart();
#else
    rtcNextReportUs = nowUs() + 60000000ULL;
    sleepNow();
#endif
  }

#if NODE_CLASS == 'C'
  if (DESCRIBE_ON_BOOT) {
    sendDescription();
    delay(500);
  }
  sendReport(false);
  useDownlink();
  radio.setPacketReceivedAction(onPacket);
  radio.startReceive();
#else
  classA(pressed);
  rtcNextReportUs = nowUs() + uint64_t(intervalS) * 1000000ULL;
  sleepNow();
#endif
}

// ---------- class C: listen all the time ----------

#if NODE_CLASS == 'C'
static void listen() {
  useDownlink();
  rxFlag = false;
  radio.setPacketReceivedAction(onPacket);
  radio.startReceive();
}

void loop() {
  uint32_t now = millis();
  if (rxFlag) {
    rxFlag = false;
    uint8_t rx[MAX_DOWNLINK + 16];
    size_t n = radio.getPacketLength();
    if (n > sizeof rx) n = sizeof rx;
    int st = radio.readData(rx, n);
    After a;
    if (st == RADIOLIB_ERR_NONE) handlePacket(rx, n, &a);
    else LOG("damaged packet (RadioLib error %d)\n", st);
    if (a.restart) {
      LOG("restarting\n");
      delay(100);
      ESP.restart();
    }
    // Wait a moment: the same broadcast may still be coming from another gateway.
    if (a.describe) {
      delay(300 + random(300));
      sendDescription();
    }
    if (a.report) {
      delay(300 + random(300));
      sendReport(false);
    }
    listen();
  }
#if NODE_TYPE == NODE_RELAY
  if (pulseUntilMs && int32_t(now - pulseUntilMs) >= 0) {
    pulseUntilMs = 0;
    setRelay(false);
    sendReport(false);
    listen();
  }
#if BUTTON_PIN >= 0
  // The local button toggles the relay.
  static bool wasDown = false;
  static uint32_t changedMs = 0;
  bool down = digitalRead(BUTTON_PIN) == (BUTTON_ACTIVE_LOW ? LOW : HIGH);
  if (down != wasDown && now - changedMs > 50) {
    wasDown = down;
    changedMs = now;
    if (down) {
      pulseUntilMs = 0;
      setRelay(!relayOn);
      sendReport(true);
      listen();
    }
  }
#endif
#endif
  if (now - lastReportMs >= uint32_t(intervalS) * 1000UL) {
    sendReport(false);
    listen();
  }
#if LED_PIN >= 0
  // The LED blinks while identifying, else shows the relay.
  if (identifyUntilMs && int32_t(now - identifyUntilMs) < 0) {
    digitalWrite(LED_PIN, (now / 150) % 2 ? HIGH : LOW);
  } else {
    identifyUntilMs = 0;
#if NODE_TYPE == NODE_RELAY
    digitalWrite(LED_PIN, relayOn ? HIGH : LOW);
#else
    digitalWrite(LED_PIN, LOW);
#endif
  }
#endif
  delay(2);
}
#else
void loop() {} // class A nodes never get here: setup() ends in deep sleep
#endif
