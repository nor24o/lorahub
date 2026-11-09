# Session log — LoRaHub

Hand-over for the next session. Read this and CLAUDE.md first. No passwords or keys
belong in this public repository.

## 1. Current state (2026-11-10, end of session 2)

### What runs where
| Device | Role | Software |
|---|---|---|
| Bobcat Miner 300, 192.168.1.15 (Armbian bookworm) | Main server | LoRaHub **0.8.16** (`/usr/local/bin/lorahub`, unit `lorahub`, data `/var/lib/lorahub`), radio engine `raw_lora_app` 2.0 (`/usr/local/bin/raw_lora_app`, unit `raw-lora`), MQTT broker :1883, web console :80, WireGuard hub `10.77.0.1` (`wg-quick@wg0`, UDP 51820), Tailscale (shares `10.77.0.0/24`, route approved) |
| Dragino LPS8, 192.168.1.17 (OpenWrt 18.06.9, AR9330, 60 MB RAM) | Satellite only | `raw_lora_app` 2.0 (same source), sends to the hub at `10.77.0.1` over WireGuard (`10.77.0.2`), LuCI page LoRa Gateway → WireGuard VPN, `/usr/bin/wg_roam.sh` from cron |
| Home Assistant, 192.168.1.13 | Consumer | Uses its own MQTT broker; LoRaHub publishes to it as the external broker and also to its own |

Older LoRaHub binaries: `/var/lib/lorahub/backup-<version>/lorahub` on the Bobcat.

### Things to know about the live system
- The Bobcat's radio stopped for about 20 minutes because **full duplex** and **radio 1 transmit** were
  switched on in Radio config; this board cannot do full duplex. The previous settings were restored; the
  failed ones are in `/etc/lora/config.json.full-duplex-failed`. Radio config now restores automatically.
- The owner deleted the CAFE group; its node recreated it with defaults. Current CAFE settings: control
  byte on (which now always adds NODE_ID + SERIAL_ID), Cayenne decoder with data offset 4 (skips
  `analog_input_1`; probably unintended), node 96B840EB class C, adopted, exposed, with its own downlink
  settings (after the node's packet, 2001 ms, 868.5 MHz SF9BW125, normal IQ).
- Measured on the real radios: the LPS8 sends scheduled (class A window) downlinks on time, 16 of 16
  test packets (`AA55AA5501`). Each SX1302 hears its own transmission on its other radio; the hub's
  echo guard ignores those.

### Verified
- `go test ./...`, including `TestEndToEnd` (`cmd/lorahub/e2e_test.go`): the whole hub against two
  simulated link-2 engines, MQTT in the role of Home Assistant and the HTTP API, 14 scenarios
  (uplink decode, two-gateway merge, unknown group, HA exposure, class A window, echo ignored, class C,
  HA buttons and sliders, per-node radio and IQ, test packets, control byte sequence, broadcast to
  class C and class A, profile byte, group delete removes HA entities). It also passed 10 runs in a row.
- On the hardware: uplinks from the CAFE node through both gateways, immediate and scheduled
  downlinks, WireGuard after an LPS8 reboot, Tailscale reaching the LPS8's web page.

### Open items
- **Router:** forward UDP 51820 to 192.168.1.15 and reserve that address, so the LPS8 can join from
  another network. The Bobcat's internet address is set on the LPS8's WireGuard VPN page (not stored here).
- **Not seen yet on a real node:** a downlink with the new `CAFE 96B840EB 8x data` header reaching the node.
- **LPS8 `pa_gpio` "dragino2:red:wan":** `raw_lora_app` switches that line back to receive about 1.4 s
  before a scheduled packet goes out (it waits only airtime + 500 ms after `lgw_send`). Measurements showed
  no loss; fix the dispatch loop if the LPS8 turns out to have an external amplifier on that line.
- The built-in MQTT broker accepts anonymous clients (owner chose to leave it for now).
- Class B is handled like class A (no ping slots).
- The LPS8 firmware image in custom-lps8-firmware does not include the WireGuard packages; they were
  installed with opkg on the device.
- Not ported from the old bridge: import of `nodes.db`. License undecided.

## 2. Deploying (from the owner's Windows PC, Go at `C:/Program Files/Go/bin`)
```
go vet ./... && go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w -X main.version=X" -o dist/lorahub-linux-arm64 ./cmd/lorahub
scp dist/lorahub-linux-arm64 root@192.168.1.15:/tmp/lorahub.new
ssh root@192.168.1.15 'mkdir -p /var/lib/lorahub/backup-OLD && cp /usr/local/bin/lorahub /var/lib/lorahub/backup-OLD/ && mv /tmp/lorahub.new /usr/local/bin/lorahub && chmod +x /usr/local/bin/lorahub && systemctl restart lorahub'
```
Check every `web/static/*.js` with `node --check` before deploying. SSH to the LPS8 needs
`-o HostKeyAlgorithms=+ssh-rsa -o PubkeyAcceptedAlgorithms=+ssh-rsa`.
Radio engine: one `raw_lora_app.c`, identical in `~/bobcat-miner-300/sx1302_hal` and
`~/2027_Latest_LORA/custom-lps8-firmware/targets/sx1302/src` (WSL Ubuntu-18.04). The LPS8 build uses the
OpenWrt toolchain `~/openwrt/openwrt/staging_dir/toolchain-mips_24kc_gcc-7.5.0_musl`; the Bobcat builds
natively against `/opt/sx1302_hal`.

## 3. What was built (both sessions)
- **Session 1:** Go hub with raw_lora_app and Semtech UDP ingest, multi-gateway dedup, NODE_ID/SERIAL
  groups and nodes, decoders, downlinks, MQTT broker, Home Assistant discovery, webhooks, REST API, SPA console.
- **Session 2 (versions 0.2 to 0.8.16):**
  - Feature survey of the Dragino UI; data pipeline, Decoder Studio, Decoder lab, schemes compatible with
    `/etc/lora/decoders.json`, automation rules, addon pages (Realtime status, Radio config, TX test,
    Remote config, Logs). SSH-based remote config removed at the owner's request.
  - Link protocol 2 between hub and engines (acknowledged UDP, signed commands, auto pairing, tx_ack);
    one unified engine for both gateways; LPS8 made a pure satellite; gateway EUI fixes.
  - Pipeline page and graphical byte-map decoder; phone layout with bottom navigation.
  - Home Assistant: values before discovery (no "unknown"), last payload sensor, Send downlink box that
    can repeat a command, per-node buttons, number and text controls (class C), stale entity cleanup,
    edits that really remove what was removed (`mergeJSON`), group delete withdraws its entities.
  - Downlinks: class C sent at once and in order, expiry of old commands, full per-node radio settings
    (gateway, frequency, SF, BW, CR, power, IQ, preamble, CRC, implicit header), node profile byte,
    control byte with sequence (always with NODE_ID + SERIAL_ID), group and everyone broadcasts with
    class A delivery in the receive window, echo guard, test packets kept out of node data,
    silent-gateway fallback, Radio config rollback.
  - WireGuard between the gateways, Tailscale on the Bobcat, WireGuard and Tailscale pages on both devices.
  - End-to-end test and `docs/node-protocol.md` (exact uplink and downlink bytes for firmware).

## 4. Owner preferences and lessons
- Act instead of asking again and again; give plain, short, step-by-step answers (Windows PC).
- No LoRaWAN in the UI. No SSH-based configuration inside the product. The LPS8 is only a satellite;
  decoding, MQTT and Home Assistant live on the Bobcat.
- Deploy to the devices after changes and verify on the real hardware, not only in tests.
- Never commit passwords, keys or the owner's internet address.
- When a user-facing setting can break the radio, check that the radio still starts.
