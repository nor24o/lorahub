# LoRaHub

**Standalone LoRa / LoRaWAN gateway processor for the Bobcat Miner 300 (G285 / RK3566).**

LoRaHub turns a modded Bobcat into a self-contained LoRa network. It runs without any cloud: it receives packets from the on-board SX1302 concentrator and from any number of satellite gateways, de-duplicates them, decrypts and decodes them, and emits **decoded JSON with per-gateway metadata**. Data goes out through a TTN-style web console, MQTT, webhooks and a REST API.

It is a single static Go binary (≈20 MB, no Python/Node runtime needed) that runs beside the C SX1302 engine `raw_lora_app` from [custom-lps8-firmware (branch `bobcat-miner-300`)](https://github.com/nor24o/custom-lps8-firmware/tree/bobcat-miner-300).

## Features

| Area | What you get |
| --- | --- |
| **Gateways** | Local Bobcat (`raw_lora_app` native protocol), **satellite gateways** via the Semtech UDP packet forwarder (LPS8, RAK, Kerlink, MikroTik, any `lora_pkt_fwd`), other Bobcats running `raw_lora_app`, and an HTTP push API for custom satellites. Gateways can register themselves automatically. You can set location, enable downlinks, override TX power, and forward raw traffic upstream (e.g. to TTN as well). |
| **LoRaWAN 1.0.x network server** | OTAA (join-accept, session-key derivation, DevNonce replay protection, EU868 CFList), ABP, MIC checks, frame-counter tracking with 32-bit rollover, FRMPayload decryption, confirmed-uplink ACKs, class A RX1/RX2 downlinks, class C downlinks, LinkCheck and DeviceTime MAC commands. Regions: EU868, US915, AU915, AS923, IN865. |
| **Raw LoRa devices** | Non-LoRaWAN nodes (like the `CAFE…` / `AA0B…` sensors) are matched by payload prefix, bytes at an offset, or a regex. They get the same decoding, storage and integrations as LoRaWAN devices. Raw downlinks can be sent immediately or after the next uplink. |
| **Multi-gateway de-duplication** | The same packet heard by several gateways becomes **one uplink**, with an `rx_metadata[]` entry for every gateway (RSSI, SNR, channel, timestamp, gateway location), sorted best first. The best gateway carries the downlink. |
| **Payload formatters** | TTN-compatible JavaScript (`decodeUplink`/`encodeDownlink`; TTN Device Repository codecs work unchanged), Cayenne LPP, and a no-code byte-field slicer. You can set a formatter per application and override it per device, with a live tester and byte map. |
| **Web console** | Modelled on The Things Stack: overview dashboard, applications, end devices (overview, live data, messaging, formatters, settings), gateways (overview, live data, transmit, settings, connection help), global live data with filters and JSON inspector, an "unknown devices" onboarding list, payload lab, concentrator (`raw_lora_app` config editor and restart), settings, API keys, and dark mode. Works on mobile. |
| **Integrations** | Embedded MQTT broker (port 1883) and/or an external broker, using TTN-style topics, downlinks over MQTT, **Home Assistant auto-discovery**, per-application webhooks with retries, and a REST API with API keys. |
| **Storage** | SQLite (pure Go, WAL), with configurable event retention. |

## Architecture

```
                 ┌──────────────── Bobcat Miner 300 (RK3566) ─────────────────┐
  LoRa nodes     │                                                             │
  (( ))──RF──► SX1302 ──SPI── raw_lora_app (C) ──UDP 5555 JSON──┐            │
                 │                     ▲            UDP 5556 ◄──┐ │            │
                 │                     │                        │ ▼            │
  Satellite      │                  LoRaHub (Go) ───────────────────────────┐  │
  gateways ──UDP 1700 (Semtech)──► │ ingest → dedup → LoRaWAN/raw → decode  │  │
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

**Sync word:** LoRaWAN devices need `"sync_word": "public"` in the concentrator config. Most raw LoRa nodes use `private`. You can change it on the **Concentrator** page.

## Connecting satellite gateways

| Satellite type | Configuration |
| --- | --- |
| Any Semtech UDP gateway | `server_address` = Bobcat IP, `serv_port_up`/`serv_port_down` = `1700` |
| Another Bobcat with `raw_lora_app` | in its `/etc/lora/config.json`: `"target_ip": "<hub ip>"`, `"port_up": 5555`, `"port_down": 5556` |
| HTTP (ESP32, LTE, scripts) | register a gateway with connection type *HTTP*, then `POST /api/v1/gateways/{eui}/uplink` with `Authorization: Bearer <gateway key>` and a body of `{"rxpk":[…]}` |

The **Connection** tab of every gateway shows ready-to-copy snippets.

## Output format

Every decoded uplink is published as JSON in the shape TTN v3 uses:

```json
{
  "end_device_ids": { "device_id": "node-1", "application_ids": { "application_id": "sensors" },
                      "dev_eui": "0004A30B001C0530", "dev_addr": "26131E5D" },
  "received_at": "2026-11-08T22:07:18.53Z",
  "uplink_message": {
    "f_port": 1, "f_cnt": 2,
    "frm_payload": "AWcBEAJofw==", "frm_payload_hex": "0167011002687F",
    "decoded_payload": { "temperature_1": 27.2, "relative_humidity_2": 63.5 },
    "rx_metadata": [
      { "gateway_ids": { "gateway_id": "eui-0016c001ff10a235", "eui": "0016C001FF10A235" }, "source": "bobcat",
        "rssi": -88.5, "snr": 7.2, "channel_index": 2, "timestamp": 123456, "location": { "latitude": 46.77, "longitude": 23.59 } },
      { "gateway_ids": { "gateway_id": "eui-1122334455667788", "eui": "1122334455667788" }, "source": "semtech_udp",
        "rssi": -110, "snr": -3 }
    ],
    "settings": { "data_rate": { "lora": { "bandwidth": 125000, "spreading_factor": 7, "coding_rate": "4/5" } }, "frequency": "868100000" },
    "consumed_airtime": "0.056576s", "confirmed": true, "gateway_count": 2, "lorawan": true
  }
}
```

### MQTT topics

| Topic | Content |
| --- | --- |
| `v3/{app}/devices/{dev}/up` | decoded uplink (above) |
| `v3/{app}/devices/{dev}/join` | join accept |
| `v3/{app}/devices/{dev}/down/queued` · `/down/sent` · `/down/failed` | downlink lifecycle |
| `v3/{app}/devices/{dev}/down/push` · `/down/replace` | **publish here** to schedule downlinks: `{"downlinks":[{"f_port":1,"frm_payload":"AQI=","confirmed":false}]}` (or `"decoded_payload":{…}`) |
| `lorahub/gateways/{gw}/up` · `/status` | raw gateway receptions / status |
| `lorahub/unmatched` | packets from unregistered devices |

The `v3` prefix is configurable. Home Assistant discovery (Settings → MQTT & Home Assistant) creates a sensor for each decoded field, plus RSSI and SNR.

### REST API

Authenticate with the session cookie or `Authorization: Bearer <API key>` (Settings → API keys). Main endpoints:

```
GET/POST        /api/applications                     GET/PUT/DELETE /api/applications/{app}
GET/POST        /api/applications/{app}/devices       GET/PUT/DELETE /api/applications/{app}/devices/{dev}
POST/DELETE     /api/applications/{app}/devices/{dev}/down   {"f_port":1,"frm_payload_hex":"0102"}
GET/POST        /api/gateways                         GET/PUT/DELETE /api/gateways/{id}
GET             /api/events?app=&dev=&gateway=&names=as.up&limit=100
WS              /api/live?app=&dev=&gateway=           (live event stream)
POST            /api/formatters/test                   POST /api/simulate
```

## Development

```bash
make test        # vet + unit/integration tests (race detector)
make run         # dev instance on http://localhost:8080 (deploy/dev.yml)
make sim         # simulated Semtech gateway + OTAA device doing join, uplinks, downlinks
```

`lorahub-sim` also sends raw payloads: `lorahub-sim -mode raw -payload CAFE0102`.

## Layout

```
cmd/lorahub         main daemon
cmd/lorahub-sim     gateway/device simulator
internal/ingest     Semtech UDP server + upstream forwarder, raw_lora_app protocol, rxpk conversion
internal/processor  dedup, LoRaWAN join/data/downlink, raw matching, simulator
internal/lorawan    frame parsing, AES-CMAC MIC, encryption, join accept
internal/formatter  JavaScript (goja), Cayenne LPP, byte fields
internal/integration MQTT broker/client, Home Assistant discovery, webhooks
internal/api        REST + websocket + auth
internal/store      SQLite persistence + in-memory cache
web/static          console SPA (vanilla JS, no build step, embedded in the binary)
deploy/             systemd unit, installer, configs
```
