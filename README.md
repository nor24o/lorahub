# LoRaHub node kit

Everything needed to build a new node for [LoRaHub](https://github.com/nor24o/lorahub),
and nothing else: one firmware for every kind of node, chosen at build time,
plus the protocol, the LoRaHub settings and a decoder. The hub itself is on the
`main` branch; this branch shares no files with it.

| Folder | What is in it |
|---|---|
| `firmware/` | The node firmware (PlatformIO, ESP32 + SX1262 or SX1276, RadioLib) |
| `firmware/platformio.ini` | 15 ready builds: 3 boards × 5 node types |
| `firmware/nodes/` | Your own nodes, one small file each (two examples) |
| `firmware/include/node_config.h` | Every build-time setting with its default |
| `firmware/lib/lorahub_proto/` | The protocol in plain C++ (also usable in other firmware) |
| `firmware/test/host/` | Tests of the protocol on a PC |
| `decoder/lorahub-node-kit.js` | The LoRaHub decoder for these nodes |
| `docs/` | [Protocol](docs/protocol.md), [settings](docs/configuration.md), [LoRaHub setup](docs/lorahub-setup.md), [boards and wiring](docs/hardware.md), [radio](docs/radio.md) |

## Node types

| Type | Class | Sends | Accepts (shown in LoRaHub and Home Assistant by itself) |
|---|---|---|---|
| `sensor_ds18b20` | A, battery | temperature, battery, interval | Interval, Identify, Restart |
| `sensor_bme280` | A, battery | temperature, humidity, pressure, battery, interval | Interval, Identify, Restart |
| `relay` | C, mains | relay ON/OFF, interval | Relay (switch), Pulse (on for N s), Interval, Identify, Restart |
| `button` | A, battery | a report for each press, presses, battery; hourly heartbeat | Interval, Identify, Restart |
| `counter` | A, battery | pulses counted (water, gas, power meters), battery | Reset count, Interval, Identify, Restart |

Boards: `heltec_v3` (Heltec WiFi LoRa 32 V3), `ttgo_lora32` (LilyGO TTGO LoRa32
V2.1), `esp32_rfm95` (any ESP32 with an RFM95 module). Any other ESP32 board:
see `firmware/nodes/example-custom-board.ini`.

Every node tells LoRaHub on its own which commands it accepts (self-description),
which class it is and where it listens (profile). Nothing about it has to be
typed into LoRaHub except the node group's options below.

## Make a node, step by step

You need [PlatformIO](https://platformio.org/install/cli) (on Windows:
`pip install platformio`) and a USB cable.

1. **Pick a NODE_ID** for this kind of node, for example `A101` for greenhouse
   sensors. All nodes with the same NODE_ID form one node group in LoRaHub;
   each node gets its own SERIAL_ID from its chip automatically.

2. **Create the node group in LoRaHub first** (console → Node groups → Add node
   group), with NODE_ID `A101`, then on its Settings tab tick:
   - Prepend NODE_ID + SERIAL_ID to downlinks
   - Nodes send a profile after SERIAL_ID
   - Add a control byte after SERIAL_ID in downlinks

   and on its Decoder tab choose JavaScript and paste `decoder/lorahub-node-kit.js`.
   Details and why: [docs/lorahub-setup.md](docs/lorahub-setup.md).

3. **Describe the node in one small file.** Copy
   `firmware/nodes/example-greenhouse.ini` to `firmware/nodes/greenhouse-2.ini`,
   rename `[env:greenhouse]` to `[env:greenhouse-2]`, pick the ready build it
   starts from (board + type) and set `NODE_ID=0xA101` and anything else from
   [docs/configuration.md](docs/configuration.md).

4. **Build and flash.** In a terminal in the `firmware` folder:

   ```bash
   pio run -e greenhouse-2 -t upload
   ```

   ```bash
   pio device monitor
   ```

   The monitor shows the node's NODE_ID and SERIAL_ID, and every packet it
   sends and hears.

5. **Adopt it in LoRaHub.** It appears on the Nodes page within one report
   interval. Adopt it, and tick "Expose to Home Assistant" to get its values
   and controls there. Its commands are listed on its page under
   "Commands from the node".

To try a ready build without your own file: `pio run -e heltec_v3_relay -t upload`
(NODE_ID `0A01`; create that group as in step 2).

## Check the protocol code on a PC

```bash
cd firmware/test/host && g++ -std=c++11 -Wall -I../../lib/lorahub_proto test_proto.cpp ../../lib/lorahub_proto/*.cpp -o test_proto && ./test_proto
```

It checks every packet against the examples in [docs/protocol.md](docs/protocol.md).
