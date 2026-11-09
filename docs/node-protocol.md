# Node packet reference

What a node sends to LoRaHub (uplink) and what LoRaHub sends to a node
(downlink), for firmware authors. Every case below is exercised by
`cmd/lorahub/e2e_test.go`, which runs the whole hub against two simulated
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
| 3-0 | free for the firmware, reported as `profile.user` |

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
| MQTT `lorahub/{group}/{node}/set`, Home Assistant buttons and Send downlink box | `ON` = `0101`, `OFF` = `0100`, else the hex sent |
| MQTT `lorahub/{group}/{node}/cmd/{key}`, Home Assistant number and text | the command's payload with `{value}` filled in, e.g. `02{value}`, 50 = `0232` |
| MQTT `lorahub/{group}/{node}/down` | JSON `{"data_hex":"0101"}` |
| Console: node Downlink tab, Broadcast tab, rules | the hex entered |

## Things that put wrong values into Home Assistant

* Changing a decoder or its data offset renames or shifts fields. Use
  MQTT & Home Assistant, Home Assistant tab, "Clean up and announce again" to remove the old sensors.
* Test packets from the console (Inject test packet) are decoded and shown with
  `"simulated": true`, but never change the node, Home Assistant or rules.
* A downlink heard back by another gateway is ignored (see above).
