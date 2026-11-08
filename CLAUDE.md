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
- Not ported from the old Python bridge: edge automation rules, import of old
  `/etc/lora/decoders.json` and `nodes.db`.
- No GitHub remote yet; owner may create one (e.g. nor24o/lorahub). License undecided.

## NEXT TASK (requested by owner)
Run from the owner's PC (same LAN as the devices):
1. SSH into the reference device at **192.168.1.17** (user `root`; owner provides the
   password — do NOT commit it). Explore its web interface and features: pages, tabs,
   fields, settings, how decoding/forwarding/MQTT/node management work, its config files
   and services.
2. Write down every feature/interface element LoRaHub is missing compared to it, agree the
   list with the owner, then implement them in LoRaHub (keep the NODE_ID|SERIAL model,
   no LoRaWAN UI).
3. Build ARM64 (`make arm64` or GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build) and deploy
   to the Bobcat at **192.168.1.15** (root): copy binary to /usr/local/bin/lorahub and
   `systemctl restart lorahub` (first install: deploy/install.sh). Verify in the browser.
4. Commit and push to github.com/nor24o/lorahub.
Note: the owner's PC is Windows; files cloned there may get CRLF line endings (.gitattributes
now forces LF). On the Bobcat fix with: sed -i 's/\r$//' deploy/*.sh
