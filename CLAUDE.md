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
- `internal/satpanel`: a Bobcat's device settings (telemetry; wired and Wi-Fi address settings; Wi-Fi client, radio
  on/off, access point bridged to the cable or with its own network 10.42.0.0/24 (NetworkManager shared mode, needs
  dnsmasq-base); host name, time zone, NTP; SD card mount/browse/download and LoRaHub backup; status lights).
  `API()` is mounted by the hub at `/api/host` (console page `#/device`, `web/static/device.js`) and by
  `cmd/lorahub-sat`, the satellite's own page on :80 (root password via `unix_chkpwd`, sessions hashed in
  `/var/lib/lorahub-sat/sessions.json`). The pages are one file, `web/static/host-ui.js`, used by both.
  Every network change is checked for 60 s and undone if the board loses its way out: a satellite pings the hub
  through the tunnel, the hub pings the home router. Changes to an active profile use `nmcli device reapply`.
  NetworkManager profiles of the access point: `lorahub-ap`, plus `lorahub-br0` and `lorahub-br0-port` for the bridge.
  `internal/wgstat` is the WireGuard parser shared with the hub's VPN page.
- `internal/ingest` Semtech UDP server/forwarder, raw_lora_app protocol (JSON rxpk on
  5555, binary heartbeat 0x02 / boot 0x03, downlink flat JSON to port 5556)
- `internal/processor` dedup across gateways → `node.go` (NODE_ID/SERIAL handling,
  output JSON, downlink header) → raw.go (downlinks) → events
- `internal/formatter` JS (goja) `decodeUplink(input)` with input.bytes = data after
  header, input.node_id, input.serial, input.payload; Cayenne LPP; byte fields
- `internal/integration` built-in MQTT broker + external, Home Assistant discovery, webhooks
- `internal/api` REST + websocket `/api/live`; `internal/store` SQLite (pure Go)
- `web/static` SPA (vanilla JS, no build step, embedded): app.js = core/components,
  pages.js = routes. help.js = Help (`#/help/<topic>`, and the satellite's Help tab): every function with an animated
  example (flow scenes, byte scenes, a live LED simulator); add a topic there when adding a feature. Routes still use `/applications` internally = node groups.
- `deploy/` install.sh, systemd unit, lorahub.yml, dev.yml

## Output JSON (MQTT `lorahub/{group}/{serial}/up`)
`node_id, serial, group, device, name, received_at, payload_hex, data_hex, decoded,
gateway_count, gateways[{gateway_id,name,eui,source,rssi,snr,channel,timestamp,location}], radio{...}`
Downlink: publish `{"data_hex":"01"}` to `lorahub/{group}/{serial}/down`; NODE_ID+SERIAL
prepended when group `downlink_header` is on; radio defaults to node's last packet.

## Commands
- `make test` (vet + race tests) · `make run` (dev on :8080) · `make sim` · `make arm64`
- End to end: `go test -run TestEndToEnd ./cmd/lorahub` starts the whole hub (`startHub` in `cmd/lorahub/hub.go`) against two
  simulated link-2 engines, MQTT and the HTTP API. Packet formats for firmware: `docs/node-protocol.md`.
- Echo guard (`internal/processor/echo.go`): every transmission goes through `p.send`; the same bytes received within 20 s are ignored.
  Console test packets (`Source: simulated`) are published with `"simulated": true` but never touch the node, HA or rules.
- Go ≥ 1.26 (go.mod). Cross-compile is pure Go (CGO_ENABLED=0).

## Status / open items
- Running on the real Bobcat (LoRaHub 0.8.27) with the LPS8 and bobcat-sat3 as satellites; current state, open items and
  owner preferences are in SESSION_LOG.md section 1.
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

Home Assistant: exposing a node publishes its last values (retained) before discovery, so entities never start unknown. Every exposed
node gets a Last payload sensor and a Send downlink text box; Device.HACommands become buttons and Device.HASwitch adds a switch. All send to `/set`.
The text box reads the retained `{prefix}/{group}/{node}/command_box` topic: empty at rest, the sent hex for 1.5 s, then empty again, because HA will not send a value equal to the entity's state.
Per-node commands are defined in LoRaHub (Device.HACommands, exposure popup) and announced to HA. Kinds (`internal/model/hacommand.go`): button (fixed payload, any class), number and text (class C only, payload contains `{value}`). Dynamic ones use `{prefix}/{group}/{node}/cmd/{key}` and `.../state`; the last number is kept in Device.HAValues.

## Gateway roles and link
The Bobcat is the main server: LoRaHub plus its own raw_lora_app. The Dragino LPS8 at .17 is only a satellite: it runs
raw_lora_app, sends uplinks to the Bobcat and receives config and downlinks. Decoding, MQTT and Home Assistant are on the Bobcat.
A second Bobcat, `bobcat-sat3` at .16 (tunnel 10.77.0.3), is also a satellite, set up from a copy of the hub's eMMC by
`deploy/satellite/add-satellite.sh` (README there). That script detects such a copy and gives it its own identity first; never
start a copied hub with LoRaHub, Tailscale or wg0 running, and never `tailscale logout` on a copy (it shares the hub's login).
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

Stale entities: `SweepHA` (every 15 s, after node edits, group deletes and settings changes) withdraws `lorahub_*` discovery topics on both
brokers that no node or gateway stands behind. Field sensors of exposed nodes are kept even if the last packet lacked them; "Clean up and
announce again" (strict) removes those too. HA is on the external broker 192.168.1.13; LoRaHub also publishes to its own.
Edits use `mergeJSON` (`internal/api/merge.go`): fields sent are replaced whole, and the stored copy used as "before" is never mutated.

## Downlink settings and node profile
Effective settings (`downlinkCfg`, `internal/processor/node.go`): Device.RawDownlink (node's Downlink tab: mode, delay, gateway, frequency, SF, BW, CR, power, IQ, preamble, CRC, implicit header, NODE_ID+SERIAL) wins outright; otherwise group `downlink_radio`, class C forcing immediate, then the firmware's profile on top.
Group option `node_profile` (`internal/model/profile.go`): after SERIAL_ID comes 1 profile byte (bits 7-6 class 00 A/01 B/10 C, bit 5 inverted IQ, bit 4 radio block follows, bit 3 description, bits 2-0 free) and an optional 4-byte radio block (frequency in 100 Hz MSB first, 0 = uplink's; then SF nibble, 2-bit BW, 2-bit CR). Removed from `data_hex` before decoding; reported as `profile`, or `profile_error` with the data left intact. Class B is handled like A (no ping slots).

Group option `downlink_control` (node override `node_control`, and it always turns the NODE_ID+SERIAL header on): downlink is `NODE_ID | SERIAL_ID | control | data`, control = bit 7 from hub, bits 6-5 audience (00 one node, 01 group, 10 everyone), bit 4 confirm, bits 3-0 sequence (Device.DownSeq). Broadcast tab (`SendBroadcast`, `internal/processor/broadcast.go`): SERIAL_ID FFFFFFFF, NODE_ID FFFF for everyone; sent once per chosen gateway in turn; class A nodes get a copy in their queue with ExpiresAt, for their next receive window.

## Node self-description
A node can announce the commands it accepts (`internal/model/describe.go`, `internal/processor/describe.go`, layout in
`docs/node-protocol.md`): an uplink with profile bit 3, then `D1`, a part byte (part/parts nibbles) and entries
(code, kind = type nibble button/switch/number/choice/raw + value format nibble, type fields, name, unit for numbers).
Without the group's profile option it is still recognised by its exact layout. Parts are collected for 10 minutes; the
result is `Device.Described` (kept on console edits, changed only by the node) and published retained on
`{prefix}/{group}/{node}/description`. `Device.Commands()` = commands set in LoRaHub, then the announced ones (as
HACommand with Source "node"; a hand-made one with the same name wins). New HACommand kinds: switch (sends code 01/00)
and choice (HA select, sends the option's value). A command whose name matches a decoded field takes its HA state from
that field. Downlink `FF` asks a node to describe itself: node page button, `POST .../devices/{dev}/describe`, or
`DESCRIBE` on `/set`. Processor tells HA through `Processor.NodeChanged` (set to `mq.NodeChanged` in hub.go).

## Tunnel between gateways
WireGuard subnet 10.77.0.0/24: Bobcat 10.77.0.1 (`wg-quick@wg0`, UDP 51820), LPS8 10.77.0.2 (OpenWrt `network.wg0`). The LPS8's raw_lora_app sends to `target_ip` 10.77.0.1. `/usr/bin/wg_roam.sh` (cron, each minute) alternates the Bobcat's LAN and public endpoints while there is no recent handshake; off-network use needs UDP 51820 forwarded to the Bobcat on the home router. Tailscale is on the Bobcat only, advertising 10.77.0.0/24; it does not fit on the LPS8 (36.7 MB download, 9.7 MB free flash, 60 MB RAM).
Pages: LoRaHub `#/vpn` with WireGuard and Tailscale tabs (`web/static/vpn.js`, `internal/api/vpn.go`; local `wg`, `systemctl`, `tailscale` only, private keys never leave the parser). LPS8: LuCI LoRa Gateway → WireGuard VPN (`view/lora/vpn.htm`, `action_vpn` in `controller/lora.lua`), which edits `network.bobcat.lan_endpoint` and `wan_endpoint` read by `wg_roam.sh`.

Radio config "apply" (`POST /api/concentrator/apply`) restarts raw-lora and puts the previous config.json back when the radio is not
LISTENING within 25 s (the failed one is kept as config.json.failed). Neither board supports `full_duplex`: it stops the Bobcat with
"FDD mode of Radio A has not been set properly". Class C downlinks avoid a gateway whose link-2 engine has not reported for 45 s.
Status lights (raw_lora_app 2.2): the engine reads `/etc/lora/led_mode` about once a second, a mode word and colours,
e.g. `steady idle=cyan rx=red tx=blue`. Modes: normal (heartbeat, packet flashes), steady (idle colour lit, flashes), quiet
(heartbeat only), solid (idle colour lit only), errors (dark unless something is wrong), off. Colours: green red blue yellow
cyan magenta white off (one RGB LED, channels on or off; on the Bobcat the sysfs names are swapped: green is `blue:status`,
blue is `green:status`). Error signals keep fast red and blinking yellow. It reports `led_mode`, `led_idle`, `led_rx`,
`led_tx` in its status. The device page writes the file from `/etc/lora/led_schedule.json` (mode, colours, night window);
"Try" (`POST /lights/preview`) shows one colour for 5 s. 2.2 runs on both Bobcats; the LPS8 still runs 2.0.
Hub DNS: the provider's DNS servers are 100.100.0.1 and 100.100.1.1, inside 100.64.0.0/10, whose answers Tailscale's
firewall drops when they do not arrive on tailscale0. The hub's Wi-Fi profile Link2 therefore uses the router
(192.168.1.1) as its only DNS server (`ipv4.ignore-auto-dns yes`).
Measured 2026-11-10: LPS8 scheduled TX goes out on time (16 of 16 probes, `scratchpad engine17/txprobe.py`); each SX1302 hears its own TX on its other radio.

## Deploying
Windows PC has Go 1.27 in `C:/Program Files/Go/bin`. Key login works on .15 and .17 (.17 needs
`-o HostKeyAlgorithms=+ssh-rsa -o PubkeyAcceptedAlgorithms=+ssh-rsa`). Copy
`dist/lorahub-linux-arm64` to `/usr/local/bin/lorahub`, then `systemctl restart lorahub`.
