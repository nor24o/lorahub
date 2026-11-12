// LoRaHub decoder for nodes built with the node kit.
//
// In LoRaHub: Node groups → your group → Decoder → JavaScript, paste this file,
// Save. input.bytes is the data after NODE_ID, SERIAL_ID and the profile.
//
// The kit sends Cayenne LPP; this names each value by its channel, so Home
// Assistant gets "temperature" and "battery" instead of "temperature_1" and
// "voltage_4", and the Relay and Interval controls show the node's real state.
// Channels and command codes: firmware/lib/lorahub_proto/kit.h.

var NAMES = {
  1: 'temperature', // °C
  2: 'humidity',    // %
  3: 'pressure',    // hPa
  4: 'battery',     // V
  5: 'relay',       // ON or OFF
  6: 'presses',     // button presses since power-on
  7: 'count',       // pulses counted
  8: 'interval',    // s between reports
  9: 'button'       // 1 when the report was sent because of a press
};

// Cayenne LPP type: [size in bytes, step, signed]
var TYPES = {
  0: [1, 1, false],     // digital input
  1: [1, 1, false],     // digital output
  2: [2, 0.01, true],   // analog input
  100: [4, 1, false],   // generic
  101: [2, 1, false],   // illuminance
  103: [2, 0.1, true],  // temperature
  104: [1, 0.5, false], // humidity
  115: [2, 0.1, false], // pressure
  116: [2, 0.01, false],// voltage
  120: [1, 1, false]    // percentage
};

function decodeUplink(input) {
  var b = input.bytes;
  var data = {};
  var warnings = [];
  var i = 0;
  while (i + 2 <= b.length) {
    var ch = b[i];
    var t = TYPES[b[i + 1]];
    if (!t) {
      warnings.push('unknown value type ' + b[i + 1] + ' at byte ' + (i + 1));
      break;
    }
    if (i + 2 + t[0] > b.length) {
      warnings.push('the value on channel ' + ch + ' is cut short');
      break;
    }
    var v = 0;
    for (var k = 0; k < t[0]; k++) v = v * 256 + b[i + 2 + k];
    if (t[2] && v >= Math.pow(2, 8 * t[0] - 1)) v -= Math.pow(2, 8 * t[0]);
    v = Math.round(v * t[1] * 1000) / 1000;
    if (ch === 5) v = v ? 'ON' : 'OFF';
    data[NAMES[ch] || 'channel_' + ch] = v;
    i += 2 + t[0];
  }
  return { data: data, warnings: warnings };
}

// Downlinks as JSON (node page → Messaging → "JSON (encoder)", or MQTT
// lorahub/{group}/{node}/down with {"decoded": {...}}):
//   {"relay": true}  {"pulse": 30}  {"interval": 600}
//   {"identify": true}  {"reset_count": true}  {"restart": true}  {"describe": true}
function encodeDownlink(input) {
  var d = input.data || {};
  function u16(v) {
    v = Math.max(0, Math.min(65535, Math.round(Number(v))));
    return [v >> 8, v & 255];
  }
  if (d.describe) return { bytes: [0xff] };
  if (d.relay !== undefined) {
    var on = d.relay === true || d.relay === 1 || d.relay === '1' || String(d.relay).toUpperCase() === 'ON';
    return { bytes: [0x01, on ? 1 : 0] };
  }
  if (d.pulse !== undefined) return { bytes: [0x02].concat(u16(d.pulse)) };
  if (d.interval !== undefined) return { bytes: [0x03].concat(u16(d.interval)) };
  if (d.identify) return { bytes: [0x04] };
  if (d.reset_count) return { bytes: [0x05] };
  if (d.restart) return { bytes: [0x06] };
  return { errors: ['use relay, pulse, interval, identify, reset_count, restart or describe'] };
}
