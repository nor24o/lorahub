# Session log — LoRaHub (first session, 2026-11-08/09)

Hand-over for the next Claude Code session. Read this + CLAUDE.md first.

## 1. What the owner wants (in his words, condensed)
- Convert a **modded Bobcat Miner 300 (G285 / RK3566, Armbian)** into a **standalone LoRa
  gateway / processor** that outputs **decoded JSON packets with metadata from the gateway(s)
  that received them**.
- **Nice, fully interactive UI with fields and tabs, like TTN.**
- Must **accept uplinks from other gateways (satellites)**.
- Concentrator reference: https://github.com/nor24o/custom-lps8-firmware/tree/bobcat-miner-300
  (C engine `raw_lora_app` drives the SX1302; old Python bridge + Flask UI).
- **Packets are NOT LoRaWAN.** Format: `NODE_ID (group, 2 bytes) | SERIAL_ID (4 bytes) | data`.
  Example: `CAFE 96B840EB 0102014A0271...` (data after byte 6 is Cayenne LPP for CAFE nodes).
  **No OTAA / DevEUI / JoinEUI / AppKey anywhere in the UI.** (Owner was very clear.)
- Code lives on GitHub **https://github.com/nor24o/lorahub** (public) and locally on the
  owner's Windows PC: `C:\Users\horva\Desktop\1_DEVELOP\LORA_GATEWAY_SEERVER_CONCENTRATOR\lorahub`.

## 2. NEXT TASK — what the owner asked for at the end (do this now)
> "i dont like your approach, it's nice but it misses so many functions. Connect to the device
> on ip 192.168.1.17 (root / password given by owner in chat) and check out its interface and
> what features I would like to have, and how; also what the interfaces contain. Then make the
> modifications and upload the code to the Bobcat at 192.168.1.15."

Steps:
1. `ssh root@192.168.1.17` (ask the owner for the password; it is NOT stored in this public repo).
   Inspect: web UI (open `http://192.168.1.17/` — every page/tab/field/button), running services
   (`systemctl list-units --type=service`, `ps`), web server files (find the web root, e.g.
   `/www`, `/usr/lib/lua/luci`, `/var/www`, `/opt/*`), config files (`/etc/lora*`, `/etc/config/*`),
   decoders, MQTT/forwarding settings, node/device management, logs, statistics, system pages.
2. Produce a **feature + interface inventory** of 192.168.1.17 and a **gap list vs LoRaHub**.
   Show it to the owner and confirm before coding.
3. Implement the missing features/pages in LoRaHub (Go backend + `web/static` SPA).
4. Build ARM64 and deploy to **192.168.1.15** (Bobcat, root):
   ```
   GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o prebuilt/lorahub-linux-arm64 ./cmd/lorahub
   scp prebuilt/lorahub-linux-arm64 root@192.168.1.15:/usr/local/bin/lorahub
   ssh root@192.168.1.15 systemctl restart lorahub
   ```
   First install on the Bobcat: `./deploy/install.sh ./prebuilt/lorahub-linux-arm64`.
   Then verify `http://192.168.1.15/` and `journalctl -u lorahub -n 50`.
5. Commit + push to GitHub, update `prebuilt/` binary.

## 3. What was built so far (LoRaHub, Go, single static binary)
- **Ingest:** raw_lora_app native protocol (UDP 5555 JSON rxpk + binary heartbeat/boot; downlinks
  as flat JSON to the sender's port 5556), Semtech UDP packet forwarder server (port 1700,
  satellites), HTTP push `/api/v1/gateways/{eui}/uplink`, upstream Semtech UDP forwarding.
  Gateways auto-register.
- **Processing:** dedup of the same packet across gateways (default 250 ms) → one message with
  all gateways' RSSI/SNR/channel/location; header split NODE_ID/SERIAL; node groups by NODE_ID;
  nodes auto-registered per SERIAL; decoders (Cayenne LPP, byte fields, JavaScript
  `decodeUplink(input)` with input.bytes = data after header, input.node_id, input.serial).
- **Downlinks:** hex or JSON (encodeDownlink), immediate or after next packet, NODE_ID+SERIAL
  header prepended, radio defaults to the node's last packet.
- **Output JSON:** `node_id, serial, group, device, name, received_at, payload_hex, data_hex,
  decoded, gateway_count, gateways[...], radio{...}`.
- **Integrations:** built-in MQTT broker :1883 (`lorahub/{group}/{serial}/up`, `.../down`),
  external MQTT, Home Assistant discovery, webhooks, REST API + websocket, API keys.
- **Web UI (port 80):** overview, node groups (overview/nodes/live/decoder/integrations/settings),
  node pages (overview/live/downlink/decoder/settings), gateways (overview/live/transmit/
  settings/connection), live data + unknown NODE_ID onboarding, decoder lab, concentrator
  config editor (edits /etc/lora/config.json, restarts raw-lora), settings, dark mode.
- **Tests:** `go test ./...` pass (incl. real CAFE packet). Verified only in a cloud sandbox with
  a simulator + headless browser. **Never yet confirmed running on the real Bobcat.**

## 4. Deployment state on the Bobcat 192.168.1.15
- Owner copied the repo to `/root/lorahub` and ran the installer → failed with
  `sudo: unable to execute ./deploy/install.sh: No such file or directory` because Windows git
  converted line endings to CRLF. Fix on the Bobcat:
  `sed -i 's/\r$//' deploy/install.sh deploy/lorahub.yml deploy/lorahub.service`
  then `./deploy/install.sh ./prebuilt/lorahub-linux-arm64`. Repo now has `.gitattributes`
  forcing LF. **Unknown whether the install was completed** — check `systemctl status lorahub`.
- Bobcat: Armbian, Debian bookworm, kernel 6.18.4-current-rockchip64, 2 GB RAM, 57 GB disk.

## 5. Lessons from this session (avoid repeating)
- The first session ran in a **cloud container**: it could not see the owner's PC or LAN
  (192.168.1.x). Work must run on the owner's PC to reach the devices.
- Ask before choosing big things: **programming language** (Go was chosen without asking — owner
  objected; confirm whether to keep Go), UI language, scope.
- Owner wants direct action and plain step-by-step instructions (Windows PowerShell + SSH),
  not long explanations.
- Don't assume LoRaWAN. Don't put credentials in this public repo.

## 6. Open questions for the owner
- Keep Go, or rewrite in another language (Python / C / Node)?
- UI language (English / Romanian / Hungarian)?
- Import old `/etc/lora/decoders.json` and `nodes.db` from the previous firmware?
- Port the old "edge automations" (rules → downlink/webhook)?
- License for the repo.
