# Setting up LoRaHub for kit nodes

Once per node group (one NODE_ID), before the first node is switched on. After
that, every new node of the group shows up by itself.

## 1. Create the group

Console → **Node groups** → **Add node group**: NODE_ID as in the node's
`NODE_ID` (for `0xA101` type `A101`), and a name.

If a node was switched on first, LoRaHub has already made the group from its
first packet: open it from Node groups instead.

## 2. Group settings

On the group's **Settings** tab, tick:

| Option | Why | Firmware setting |
|---|---|---|
| Add new nodes automatically | each new board appears without typing its SERIAL_ID | — |
| Prepend NODE_ID + SERIAL_ID to downlinks | the node knows a downlink is for it | `DOWNLINK_HEADER=1` |
| Nodes send a profile after SERIAL_ID | LoRaHub learns the node's class and where it listens, and removes the profile before decoding | `SEND_PROFILE=1` |
| Add a control byte after SERIAL_ID in downlinks | the node tells commands from other packets, ignores repeats, and is asked to confirm | `DOWNLINK_CONTROL=1` |

The firmware and the group must agree. When they do not:

| Group says | Node built with | What happens |
|---|---|---|
| no profile | `SEND_PROFILE=1` | the profile byte is decoded as data: wrong values |
| profile | `SEND_PROFILE=0` | the first data byte is read as a profile |
| no control byte | `DOWNLINK_CONTROL=1` | the node ignores every command |
| control byte | `DOWNLINK_CONTROL=0` | the control byte is read as the command: nothing works |

Leave the group's downlink radio settings empty: each node announces its own
(class C nodes listen on 869.525 MHz SF9 by default).

## 3. Decoder

Group → **Decoder** tab → **JavaScript**, paste all of `decoder/lorahub-node-kit.js`,
**Save**. Values then get plain names (`temperature`, `battery`, `relay`,
`count`), and the Relay and Interval controls show the node's real state.

## 4. Each node

Switch the node on. Within its first report it is on the **Nodes** page:

- **Adopt** it and give it a name. The class comes from the node itself.
- Its page shows **Commands from the node**: what it announced at start.
  **Ask the node to describe itself** asks again (`FF`); a class A node
  answers after its next report.
- To have it in Home Assistant, tick **Expose to Home Assistant** (in the adopt
  window, or MQTT & Home Assistant → exposure). Sensors, and the announced
  controls (switch, sliders, buttons), appear with their values already set.

## What you can send

From the node page (Messaging), Home Assistant, rules or MQTT:

| To | Send |
|---|---|
| Relay on / off | Home Assistant switch, or MQTT `ON` / `OFF` on `lorahub/{group}/{node}/set` |
| Relay on for 30 s | Pulse slider, or hex `02001E` |
| Report every 10 min | Interval slider, or hex `030258` |
| Blink the LED | Identify button, or hex `04` |
| Reset a counter | Reset count button, or hex `05` |
| List its commands again | `DESCRIBE` on `/set`, or hex `FF` |

Class C nodes (relays) get commands at once. Class A nodes get them right after
their next report, so a sensor reporting every 5 minutes reacts within 5
minutes; the Overview's **Queued downlinks** card shows what is waiting.
