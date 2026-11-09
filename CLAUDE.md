# CLAUDE.md — project context for Claude Code

## What this is
LoRaHub: standalone LoRa gateway processor + TTN-style web console for a modded
**Bobcat Miner 300 (G285 / RK3566)**. Single static Go binary. Runs next to the C
SX1302 engine `raw_lora_app` from https://github.com/nor24o/custom-lps8-firmware
(branch `bobcat-miner-300`) and replaces that repo's Python bridge + Flask UI.

## Owner's requirements (do not deviate)
- **No LoRaWAN in the UI.** No OTAA/ABP, DevEUI, JoinEUI, AppKey. The owner was explicit.
  LoRaWAN code still exists (internal/lorawan, processor/lorawan.go) but is disabled
  (`Settings.LoRaWANEnabled=false`) and hidden. Don't resurface it.
- **Packet format:** `NODE_ID (2 bytes) | SERIAL_ID (4 bytes) | data`, e.g.
  `CAFE 96B840EB 0102014A...`. Offsets/lengths configurable (Settings → Packet format).
- NODE_ID = **node group** (stored as `model.Application`, field `NodeID`).
  SERIAL_ID = **node** (`model.Device`, `Mode:"node"`, field `Serial`), auto-registered.
- Output = decoded JSON + metadata from **every gateway** that heard the packet.
- Must accept uplinks from **satellite gateways** (Semtech UDP :1700, remote
  raw_lora_app :5555, HTTP push).
- Nice, fully interactive TTN-like UI with tabs.
- Owner works in `C:\Users\horva\Desktop\1_DEVELOP\LORA_GATEWAY_SEERVER_CONCENTRATOR`
  (Windows). Explain where files are and how to run things in plain steps.

## Layout
- `cmd/lorahub` daemon · `cmd/lorahub-sim` gateway simulator (default sends CAFE packets)
- `internal/ingest` Semtech UDP server/forwarder, raw_lora_app protocol (JSON rxpk on
  5555, binary heartbeat 0x02 / boot 0x03, downlink flat JSON to port 5556)
- `internal/processor` dedup across gateways → `node.go` (NODE_ID/SERIAL handling,
  output JSON, downlink header) → raw.go (downlinks) → events
- `internal/formatter` JS (goja) `decodeUplink(input)` with input.bytes = data after
  header, input.node_id, input.serial, input.payload; Cayenne LPP; byte fields
- `internal/integration` built-in MQTT broker + external, Home Assistant discovery, webhooks
- `internal/api` REST + websocket `/api/live`; `internal/store` SQLite (pure Go)
- `web/static` SPA (vanilla JS, no build step, embedded): app.js = core/components,
  pages.js = routes. Routes still use `/applications` internally = node groups.
- `deploy/` install.sh, systemd unit, lorahub.yml, dev.yml

## Output JSON (MQTT `lorahub/{group}/{serial}/up`)
`node_id, serial, group, device, name, received_at, payload_hex, data_hex, decoded,
gateway_count, gateways[{gateway_id,name,eui,source,rssi,snr,channel,timestamp,location}], radio{...}`
Downlink: publish `{"data_hex":"01"}` to `lorahub/{group}/{serial}/down`; NODE_ID+SERIAL
prepended when group `downlink_header` is on; radio defaults to node's last packet.

## Commands
- `make test` (vet + race tests) · `make run` (dev on :8080) · `make sim` · `make arm64`
- Go ≥ 1.26 (go.mod). Cross-compile is pure Go (CGO_ENABLED=0).

## Status / open items
- Verified only in a cloud sandbox with the simulator + headless browser; **not yet run
  on the real Bobcat** (check downlink timing, raw_lora_app port 5556, sync word `private`).
- Not ported from the old bridge: import of old `nodes.db`.
- GitHub: https://github.com/nor24o/lorahub (public). License undecided. Full history: SESSION_LOG.md

## Data pipeline (added after surveying the Dragino reference at 192.168.1.17)
- Gather: unknown NODE_IDs auto-create groups and appear on `#/nodes` for adoption. A prefix
  scheme with data before byte 6 (or `serial: none`) identifies a node by NODE_ID alone, so
  `NODE|TYPE|SEQ|data` packets do not create one node per sequence number.
- Decode: `internal/formatter/fields.go` (field types and options), `methods.go` (TLV, bit-packed, CSV, key=value, regex, JSON, CBOR, MessagePack, preprocessing), `schemes.go`: ordered schemes, first match that decodes wins; JSON
  compatible with `/etc/lora/decoders.json`, imported on first start. A decoder set on a node or
  group has priority. UI: `web/static/hub.js` (Nodes, Decoder Studio, Automation, Messages tab).
- Output: `lorahub/{group}/{serial}/up` (full) and retained flat `/state`; commands on `/set`
  (ON, OFF or hex). Home Assistant discovery is per node: adopted + ha_enabled only
  (`internal/integration/homeassistant.go`). Rules: `internal/processor/rules.go`.

## Addon pages (web/static/addons.js, integrations.js, lab.js, internal/api/addons.go, remote.go)
Realtime status, Radio config, TX test, Remote config, Logs, Decoder lab, MQTT & Home Assistant (publishing, commands,
per-entity exposure). GPIO and Pinout pages were removed on request.
Remote config does NOT use SSH (owner's requirement); it uses the raw_lora_app link below.

Pipeline page (web/static/pipeline.js): live flow of packets through gateways, merge, decoder, node and outputs, built from
as.up and gs.up events and the current settings. Graphical decoder (web/static/bytemap.js): drag across bytes to create fields,
used by Decoder Studio, Decoder lab and the node and group Decoder tabs.

## Gateway roles and link
The Bobcat is the main server: LoRaHub plus its own raw_lora_app. The Dragino LPS8 at .17 is only a satellite: it runs
raw_lora_app, sends uplinks to the Bobcat and receives config and downlinks. Decoding, MQTT and Home Assistant are on the Bobcat.
One raw_lora_app.c serves both (GATEWAY_MODEL comes from the CPU); keep the copies in both firmware trees identical:
~/bobcat-miner-300/sx1302_hal (built natively on the Bobcat) and ~/2027_Latest_LORA/custom-lps8-firmware/targets/sx1302/src
(WSL Ubuntu-18.04, toolchain ~/openwrt/openwrt/staging_dir/toolchain-mips_24kc_gcc-7.5.0_musl).
Link protocol 2 (internal/ingest/link.go), UDP for latency: uplink and status datagrams carry seq and boot, the hub replies
`{"msg_type":"ack","seq":N}`, the engine repeats unacknowledged ones for about 12 s, the hub drops duplicates. Hub to gateway
commands are `L2:<nonce>:<hmac-sha256>:<json>`, nonce published in status; link_key is in config.json on the gateway and
Settings.LinkKey on the hub, paired automatically with `set_link_key` when the gateway has none. To re-pair after losing the hub's key,
remove link_key from the gateway's config.json and restart raw_lora_app. get_config with full:true returns the config file;
set_config merges any settings and restores the previous ones if the radio will not start. Downlinks are confirmed with tx_ack.
Engines without link 2 keep working with plain datagrams.

## Deploying
Windows PC has Go 1.27 in `C:/Program Files/Go/bin`. Key login works on .15 and .17 (.17 needs
`-o HostKeyAlgorithms=+ssh-rsa -o PubkeyAcceptedAlgorithms=+ssh-rsa`). Copy
`dist/lorahub-linux-arm64` to `/usr/local/bin/lorahub`, then `systemctl restart lorahub`.
