# Node protocol

This is LoRaHub's `docs/node-protocol.md`; the node kit follows it. The kit's own choices on top of it (channels, command codes) are at the end.

What a node sends to LoRaHub (uplink) and what LoRaHub sends to a node
(downlink), for firmware authors. Every case below is exercised by
`cmd/lorahub/e2e_test.go` in the LoRaHub repository, which runs the whole hub against two simulated
gateways. Examples use group `CAFE` and node `96B840EB`.

All values are bytes on the air, written in hex, most significant byte first.

## Uplink: node to hub

### Basic packet (always)

```
NODE_ID (2) | SERIAL_ID (4) | data
CAFE          96B840EB        0167011002
```

* `NODE_ID` picks the node group, `SERIAL_ID` the node. A new `NODE_ID`
  creates a group and a new `SERIAL_ID` a node, unless that is turned off.
* `data` goes to the decoder of the node, else of its group, else the decoder
  schemes. Example above with the Cayenne LPP decoder: `temperature_1 = 27.2`.
* Send with normal IQ (not inverted), on any channel and data rate the
  gateways listen on. The hub measures frequency, SF, bandwidth, RSSI and SNR
  of every packet; nothing about the uplink radio has to be declared.
* Keep field names stable. Every decoded field becomes a sensor in Home
  Assistant, so a packet whose fields are named differently adds new sensors.

### With the profile (group setting "Nodes send a profile after SERIAL_ID")

```
NODE_ID | SERIAL_ID | profile (1) | radio block (4, optional) | data
CAFE      96B840EB    90            84ADD2 90                   0167011002
```

| Profile bit | Meaning |
|---|---|
| 7-6 | class: `00` A, `01` B, `10` C |
| 5 | `1` = the node listens with inverted IQ |
| 4 | `1` = a radio block follows |
| 3 | `1` = this packet describes the node's commands (see below) |
| 2-0 | free for the firmware, reported as `profile.user` |

| Radio block | Meaning (how the node listens for downlinks) |
|---|---|
| bytes 0-2 | frequency in 100 Hz, `0` = same as the uplink (`84ADD2` = 869.525 MHz) |
| byte 3, bits 7-4 | SF 5 to 12, `0` = same as the uplink |
| byte 3, bits 3-2 | bandwidth: `0` 125, `1` 250, `2` 500 kHz |
| byte 3, bits 1-0 | coding rate: `0` 4/5, `1` 4/6, `2` 4/7, `3` 4/8 |

The profile and radio block are removed before decoding (`data_hex` above is
`0167011002`). A profile the hub cannot read leaves the data untouched and is
reported as `profile_error`. Settings saved on the node in the console win over
the profile; the profile wins over the group.

### Self-description: the node lists the commands it accepts

A node can tell LoRaHub which downlink commands it understands. LoRaHub then
shows them on the node's page and turns them into controls in Home Assistant
(switch, button, slider, list), so nothing has to be set up by hand.

Send a description after power-on, and again whenever the hub asks: a downlink
whose data is the single byte `FF` (console: node page, "Ask the node to
describe itself"; MQTT: `DESCRIBE` on `lorahub/{group}/{node}/set`). Do not
send it with every reading: it costs airtime.

```
NODE_ID | SERIAL_ID | profile | [radio block] | D1 | part | entries
CAFE      96B840EB    88                        D1   11     01 11 05 "Relay" 02 00 05 "Blink" 03 23 000A 0E10 00 08 "Interval" 01 "s"
```

* Profile bit 3 marks the description (`88` = class C + description). Turn on
  the group's profile option; without it LoRaHub still recognises a description
  by its exact layout, but with it a bad one is reported instead of being read
  as data.
* `D1`: description, format 1.
* `part`: bits 7-4 the part number (1-15), bits 3-0 the number of parts. A long
  list may be split; send each part as its own uplink, whole entries only, all
  within 10 minutes. `11` = part 1 of 1.

Every entry starts with a code and a kind byte and ends with a name:

| Byte | Meaning |
|---|---|
| code | the first data byte of this command's downlink, `00`-`FE` (`FF` is the describe request) |
| kind | bits 7-4 type, bits 3-0 value format |
| ... | fields of the type, below |
| name | length byte, then 1-24 ASCII characters |
| unit | numbers only, after the name: length byte, then 0-8 characters |

| Type | Fields | Becomes | Downlink |
|---|---|---|---|
| `0` button | value, only when a format is given | button | `code [value]` |
| `1` switch | none | switch | `code 01` on, `code 00` off (u8 unless another format) |
| `2` number | min, max (in the value format), scale (1 signed byte) | slider | `code value`, raw = shown ÷ 10^scale |
| `3` choice | count (1-32), then per option: value, name (length + 1-16 characters) | list | `code value` of the option |
| `4` raw | maximum length in bytes, `0` = any | hex text box | `code bytes` |

Value formats: `0` none, `1` u8, `2` i8, `3` u16, `4` i16, `5` u32, `6` i32,
big-endian. Scale is a power of ten: `FF` (-1) means a raw 215 is shown as 21.5,
so a setpoint of 21.5 °C is sent as `00D7`.

The example above, entry by entry:

| Entry | Bytes | Command | Its downlinks |
|---|---|---|---|
| Relay | `01 11 05 52656C6179` | switch, u8 | on `01 01`, off `01 00` |
| Blink | `02 00 05 426C696E6B` | button | `02` |
| Interval | `03 23 000A 0E10 00 08 496E74657276616C 01 73` | number u16, 10 to 3600, unit s | 300 s = `03 012C` |

The whole uplink is 43 bytes: about 2.1 s on air at SF12 and 0.3 s at SF9. The
largest packet is 51 bytes at SF10-SF12 and 115 bytes at SF9 (EU868), so split
longer lists into parts. Names cost airtime: keep them short.

Downlinks for these commands are framed like any other (header, control byte),
for example relay on to this class C node: `CAFE 96B840EB 84 0101`. To show the
real state in Home Assistant, let the decoder of your normal uplinks output a
field named like the command (lower case, spaces as `_`): `relay`, `interval`.
Without such a field the control shows the last value sent. Switches, sliders,
lists and text boxes appear for class C nodes; buttons for every class. A
command set by hand in the console with the same name wins.

Firmware example in C:

```c
#include <stdint.h>
#include <string.h>

static uint8_t *put_text(uint8_t *p, const char *s) {
    size_t n = strlen(s);
    *p++ = (uint8_t)n;
    memcpy(p, s, n);
    return p + n;
}

/* The description uplink: after power-on, and when the hub sends FF. */
size_t build_description(uint8_t *buf, const uint8_t node_id[2], const uint8_t serial[4]) {
    uint8_t *p = buf;
    memcpy(p, node_id, 2); p += 2;
    memcpy(p, serial, 4);  p += 4;
    *p++ = 0x88;                       /* profile: class C, description */
    *p++ = 0xD1;                       /* description, format 1 */
    *p++ = 0x11;                       /* part 1 of 1 */
    *p++ = 0x01; *p++ = 0x11;          /* code 01: switch, u8 */
    p = put_text(p, "Relay");
    *p++ = 0x02; *p++ = 0x00;          /* code 02: button, no value */
    p = put_text(p, "Blink");
    *p++ = 0x03; *p++ = 0x23;          /* code 03: number, u16 */
    *p++ = 0x00; *p++ = 0x0A;          /* min 10 */
    *p++ = 0x0E; *p++ = 0x10;          /* max 3600 */
    *p++ = 0x00;                       /* scale 10^0 */
    p = put_text(p, "Interval");
    p = put_text(p, "s");
    return (size_t)(p - buf);          /* 43 bytes */
}

/* A downlink for this node, after NODE_ID, SERIAL_ID and the control byte. */
void on_command(const uint8_t *d, size_t n) {
    if (n == 0) return;
    switch (d[0]) {
    case 0xFF: send_description(); break;
    case 0x01: if (n >= 2) relay_set(d[1] != 0); break;
    case 0x02: blink(); break;
    case 0x03: if (n >= 3) set_interval((uint16_t)(d[1] << 8 | d[2])); break;
    }
}
```

LoRaHub publishes the description, retained, on
`lorahub/{group}/{node}/description` and keeps the last one on the node. It
changes only when the node sends a new one.

### What the hub publishes for an uplink

`lorahub/{group}/{node}/up`: `node_id`, `serial`, `data_hex`, `decoded`,
`gateway_count`, `gateways[]` (best signal first), `radio`, `profile` when sent.
`lorahub/{group}/{node}/state` (retained): flat latest values, read by Home Assistant.

## Downlink: hub to node

### Bytes on the air

| Group or node setting | Bytes |
|---|---|
| Header on (default for a group you create) | `NODE_ID SERIAL_ID data` = `CAFE 96B840EB 0101` |
| Header off | `data` = `0101` |
| Control byte on (always brings the header) | `NODE_ID SERIAL_ID control data` = `CAFE 96B840EB 80 0101` |
| Broadcast to the group | `NODE_ID FFFFFFFF control data` = `CAFE FFFFFFFF A0 FF` |
| Broadcast to everyone | `FFFF FFFFFFFF control data` = `FFFF FFFFFFFF C0 FF` |

| Control bit | Meaning |
|---|---|
| 7 | always `1`: from the hub |
| 6-5 | `00` this node, `01` whole group, `10` everyone |
| 4 | `1` = please confirm (send an uplink back) |
| 3-0 | sequence number 0-15; direct messages and broadcasts count separately |

There is no port, counter, message type, signature or encryption besides the
above. Firmware should accept a packet when bit 7 of the control byte is set,
`NODE_ID` is its own or `FFFF`, and `SERIAL_ID` is its own or `FFFFFFFF`, and
ignore a sequence number it has just seen.

### When and how it is sent

| Node | When | Radio |
|---|---|---|
| Class A | Right after the node's next uplink: the uplink's end time plus the delay (default 1 s), through the gateway that heard it best | Settings, else the uplink's own channel and data rate |
| Class B | Like class A (ping slots are not scheduled) | Like class A |
| Class C | Immediately, through the gateway that heard the node best last (or the one chosen for the node) | Settings, else the channel and data rate of the node's last uplink. Give class C nodes a fixed frequency. |
| Broadcast, class C | Immediately, through each chosen gateway in turn, with the same sequence number | Frequency and data rate chosen for the broadcast |
| Broadcast, class A | In each node's next receive window, until it expires (default 60 min) | Like class A |

IQ is normal unless the node (or its profile, or its group) says inverted. With
normal IQ the other gateways hear the downlink as if it were a packet; the hub
recognises its own transmissions for 20 s and ignores them.

### Where downlinks come from

| Source | Bytes it gives (before the header) |
|---|---|
| MQTT `lorahub/{group}/{node}/set`, Home Assistant buttons and Send downlink box | `ON` = `0101`, `OFF` = `0100`, `DESCRIBE` = `FF`, else the hex sent |
| MQTT `lorahub/{group}/{node}/cmd/{key}`, Home Assistant number, text, switch and list | the command's payload with `{value}` filled in, e.g. `02{value}`, 50 = `0232`; a node's own commands: `code value` |
| Console: node page, "Ask the node to describe itself" | `FF` |
| MQTT `lorahub/{group}/{node}/down` | JSON `{"data_hex":"0101"}` |
| Console: node Downlink tab, Broadcast tab, rules | the hex entered |

## Things that put wrong values into Home Assistant

* Changing a decoder or its data offset renames or shifts fields. Use
  MQTT & Home Assistant, Home Assistant tab, "Clean up and announce again" to remove the old sensors.
* Test packets from the console (Inject test packet) are decoded and shown with
  `"simulated": true`, but never change the node, Home Assistant or rules.
* A downlink heard back by another gateway is ignored (see above).

## The node kit's choices

The protocol leaves the data bytes and the command codes to the firmware. The
kit uses these, in `firmware/lib/lorahub_proto/kit.h` and
`decoder/lorahub-node-kit.js`.

### Report data: Cayenne LPP, one channel per value

| Channel | LPP type | Field after the kit decoder | Unit |
|---|---|---|---|
| 1 | 103 temperature | `temperature` | °C, 0.1 steps |
| 2 | 104 humidity | `humidity` | %, 0.5 steps |
| 3 | 115 pressure | `pressure` | hPa, 0.1 steps |
| 4 | 116 voltage | `battery` | V, 0.01 steps |
| 5 | 1 digital output | `relay` | `ON` or `OFF` |
| 6 | 100 generic | `presses` | presses since power-on |
| 7 | 100 generic | `count` | pulses, kept through sleep and restarts |
| 8 | 100 generic | `interval` | s between reports |
| 9 | 0 digital input | `button` | `1` when the report was sent because of a press |

With LoRaHub's built-in Cayenne LPP decoder instead, the same values are named
`temperature_1`, `voltage_4`, `digital_output_5` and so on, and the Relay and
Interval controls cannot show the node's real state.

Example, a relay node reporting with its profile and radio block:

```
CAFE 00000012 90 84ADD2 90 | 04 74 0188 | 05 01 01 | 08 64 0000012C
                             battery      relay ON   interval 300 s
                             3.92 V
```

### Commands

| Code | Command | Downlink data | Types |
|---|---|---|---|
| `01` | Relay (switch) | `01 01` on, `01 00` off; also MQTT `ON` / `OFF` on `/set` | relay |
| `02` | Pulse (number, s) | `02` + u16: on, off again after that many seconds | relay |
| `03` | Interval (number, s) | `03` + u16, kept across restarts | all |
| `04` | Identify (button) | `04`: the LED blinks for a few seconds | boards with an LED |
| `05` | Reset count (button) | `05` | counter |
| `06` | Restart (button) | `06` | all |
| `FF` | describe yourself | `FF` | all |

After a command the node sends a fresh report, so LoRaHub and Home Assistant
see the new state at once. A class A node then listens again, which delivers
the next command waiting in its queue.

The decoder file also has an encoder, so the node page's Messaging tab and the
MQTT `/down` topic (as `{"decoded": {...}}`) accept JSON: `{"relay": true}`, `{"pulse": 30}`,
`{"interval": 600}`, `{"identify": true}`, `{"reset_count": true}`,
`{"restart": true}`, `{"describe": true}`.
