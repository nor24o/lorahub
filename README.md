# LoRaHub

**Standalone LoRa gateway processor for the Bobcat Miner 300 (G285 / RK3566).**

LoRaHub turns a modded Bobcat into a self-contained LoRa network. It runs without any cloud: it receives packets from the on-board SX1302 concentrator and from any number of satellite gateways, de-duplicates them, decrypts and decodes them, and emits **decoded JSON with per-gateway metadata**. Data goes out through a TTN-style web console, MQTT, webhooks and a REST API.

It is a single static Go binary (≈20 MB, no Python/Node runtime needed) that runs beside the C SX1302 engine `raw_lora_app` from [custom-lps8-firmware (branch `bobcat-miner-300`)](https://github.com/nor24o/custom-lps8-firmware/tree/bobcat-miner-300).

## How packets are handled

Every packet from your nodes looks like this:

```
| NODE_ID (2 bytes) | SERIAL_ID (4 bytes) | data ... |
   CAFE                96B840EB              0102014A0367010F
```

- **Node group** = one NODE_ID (e.g. `CAFE`). It has one decoder for the data bytes, its webhooks and its downlink settings.
- **Node** = one SERIAL_ID inside a group. Nodes are **added automatically** the first time they transmit.
- Unknown NODE_IDs appear under **Live data → Unknown NODE_IDs**, with an "Add group" button.
- Offsets and lengths of NODE_ID, SERIAL_ID and data are configurable (Settings → Packet format).
- No LoRaWAN, no DevEUI/JoinEUI/AppKey. (LoRaWAN support still exists in the code but is switched off and hidden.)

## Features

| Area | What you get |
| --- | --- |
| **Gateways** | Local Bobcat (`raw_lora_app`), **satellite gateways** over Semtech UDP (LPS8, RAK, any `lora_pkt_fwd`), other Bobcats running `raw_lora_app`, and HTTP push. Auto-registration, location, TX power, test transmit, upstream forwarding. |
| **Multi-gateway** | The same packet heard by several gateways becomes **one message** listing every gateway (RSSI, SNR, channel, timestamp, location), best first. Downlinks go through the best gateway. |
| **Decoders** | Cayenne LPP, a no-code byte-field slicer, or JavaScript `decodeUplink(input)` (input.bytes = data after the header, input.node_id, input.serial). Set per group, override per node, live tester. |
| **Downlinks** | Hex or JSON (via `encodeDownlink`), sent immediately or right after the node's next packet. NODE_ID + SERIAL_ID are prepended automatically (can be turned off). Default radio = the node's last packet's frequency/data rate. |
| **Web console** | Overview, node groups, nodes, gateways, live data with JSON inspector, decoder lab, concentrator config editor, settings, API keys, dark mode, mobile. |
| **Integrations** | Built-in MQTT broker (:1883) and/or external broker, Home Assistant auto-discovery, webhooks, REST API + websocket. |

## Architecture

```
                 ┌──────────────── Bobcat Miner 300 (RK3566) ─────────────────┐
  LoRa nodes     │                                                             │
  (( ))──RF──► SX1302 ──SPI── raw_lora_app (C) ──UDP 5555 JSON──┐            │
                 │                     ▲            UDP 5556 ◄──┐ │            │
                 │                     │                        │ ▼            │
  Satellite      │                  LoRaHub (Go) ───────────────────────────┐  │
  gateways ──UDP 1700 (Semtech)──► │ ingest → dedup → NODE_ID/SERIAL → decode │  │
  Other Bobcats ─UDP 5555────────► │   ↓          ↓                ↓       │  │
  Custom ───HTTP /api/v1/…/uplink► │ SQLite   downlink sched.   events bus  │  │
                 │                  └──┬──────────┬──────────────┬──────────┘  │
                 └─────────────────────┼──────────┼──────────────┼─────────────┘
                              Web console :80   MQTT :1883    Webhooks / REST
                                                (+ Home Assistant)
```

## Install on the Bobcat

Prerequisite: the SX1302 engine from `custom-lps8-firmware` (branch `bobcat-miner-300`) is installed and `raw-lora.service` runs. Its `/etc/lora/config.json` must have `"port_up": 5555` and `"port_down": 5556`, which are the defaults.

```bash
# on your PC: build the ARM64 binary (or download a release tarball)
make arm64
scp -r dist/lorahub-linux-arm64 deploy root@<bobcat-ip>:/tmp/lorahub
```

```bash
# on the Bobcat
cd /tmp/lorahub && sudo ./deploy/install.sh ./lorahub-linux-arm64
```

The installer disables the old `bobcat-lora-bridge` and `bobcat-web` services, which used ports 5555 and 80. It then installs `/usr/local/bin/lorahub`, `/etc/lorahub/lorahub.yml` and `lorahub.service`. Open `http://<bobcat-ip>/` and create the admin account.

> `raw_lora_app` always mirrors uplinks to `127.0.0.1:5555`, so LoRaHub receives the local concentrator traffic even when `target_ip` points elsewhere.

**Sync word:** keep `"sync_word": "private"` (0x12) in `/etc/lora/config.json` — that is what your nodes use.

## Connecting satellite gateways

| Satellite type | Configuration |
| --- | --- |
| Any Semtech UDP gateway | `server_address` = Bobcat IP, `serv_port_up`/`serv_port_down` = `1700` |
| Another Bobcat with `raw_lora_app` | in its `/etc/lora/config.json`: `"target_ip": "<hub ip>"`, `"port_up": 5555`, `"port_down": 5556` |
| HTTP (ESP32, LTE, scripts) | register a gateway with connection type *HTTP*, then `POST /api/v1/gateways/{eui}/uplink` with `Authorization: Bearer <gateway key>` and a body of `{"rxpk":[…]}` |

The **Connection** tab of every gateway shows ready-to-copy snippets.

## Output format

Every decoded packet (MQTT `lorahub/{group}/{serial}/up`, webhooks, API, live data):

```json
{
  "node_id": "CAFE",
  "serial": "96B840EB",
  "group": "node-cafe",
  "device": "96b840eb",
  "name": "CAFE-96B840EB",
  "received_at": "2026-11-08T22:34:27.78Z",
  "payload_hex": "CAFE96B840EB0102014A0367010F",
  "data_hex": "0102014A0367010F",
  "decoded": { "analog_input_1": 3.3, "temperature_3": 27.1 },
  "gateway_count": 2,
  "gateways": [
    { "gateway_id": "eui-0016c001ff10a235", "name": "Bobcat 10A235", "eui": "0016C001FF10A235", "source": "bobcat",
      "rssi": -88.5, "snr": 7.2, "channel": 2, "timestamp": 123456, "location": { "latitude": 46.77, "longitude": 23.59 } },
    { "gateway_id": "eui-1122334455667788", "name": "Roof LPS8", "eui": "1122334455667788", "source": "semtech_udp",
      "rssi": -110, "snr": -3, "channel": 2 }
  ],
  "radio": { "frequency": 868100000, "data_rate": "SF7BW125", "spreading_factor": 7, "bandwidth_khz": 125, "coding_rate": "4/5", "airtime_ms": 46.33 }
}
```

### MQTT topics (prefix `lorahub`, configurable)

| Topic | Content |
| --- | --- |
| `lorahub/{group}/{serial}/up` | decoded packet (above) |
| `lorahub/{group}/{serial}/down` | **publish here** to send a downlink: `{"data_hex":"01"}` or `{"decoded":{"relay":true}}` |
| `lorahub/{group}/{serial}/down/queued` · `/down/sent` · `/down/failed` | downlink status |
| `lorahub/unmatched` | packets from unknown NODE_IDs |
| `lorahub/gateways/{gateway}/up` · `/status` | raw gateway receptions / status |

### REST API

Authenticate with the session cookie or `Authorization: Bearer <API key>` (Settings → API keys). Main endpoints:

```
GET/POST        /api/applications                     (node groups; body {"node_id":"CAFE","name":"…"})
GET/PUT/DELETE  /api/applications/{group}
GET/POST        /api/applications/{group}/devices     (nodes; body {"serial":"96B840EB"})
GET/PUT/DELETE  /api/applications/{group}/devices/{serial}
POST/DELETE     /api/applications/{group}/devices/{serial}/down   {"frm_payload_hex":"01"}
GET/POST        /api/gateways                         GET/PUT/DELETE /api/gateways/{id}
GET             /api/events?app=&dev=&gateway=&names=as.up&limit=100
WS              /api/live?app=&dev=&gateway=           (live event stream)
POST            /api/formatters/test                   POST /api/simulate
```

## Development

```bash
make test        # vet + unit/integration tests (race detector)
make run         # dev instance on http://localhost:8080 (deploy/dev.yml)
make sim         # simulated gateway sending CAFE / 96B840EB packets
```

Send any packet: `lorahub-sim -payload CAFE96B840EB0102014A -count 5`.

## Layout

```
cmd/lorahub         main daemon
cmd/lorahub-sim     gateway/device simulator
internal/ingest     Semtech UDP server + upstream forwarder, raw_lora_app protocol, rxpk conversion
internal/processor  dedup, NODE_ID/SERIAL node handling, downlinks, simulator (+ dormant LoRaWAN)
internal/lorawan    frame parsing, AES-CMAC MIC, encryption, join accept
internal/formatter  JavaScript (goja), Cayenne LPP, byte fields
internal/integration MQTT broker/client, Home Assistant discovery, webhooks
internal/api        REST + websocket + auth
internal/store      SQLite persistence + in-memory cache
web/static          console SPA (vanilla JS, no build step, embedded in the binary)
deploy/             systemd unit, installer, configs
```
