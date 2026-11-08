#!/bin/sh
# LoRaHub installer for the Bobcat Miner 300 (G285 / RK3566) running Armbian.
# Usage: sudo ./install.sh [path-to-lorahub-binary]
# Expects raw_lora_app (custom-lps8-firmware, branch bobcat-miner-300) to be
# installed already; LoRaHub replaces bobcat-lora-bridge and bobcat-web.
set -eu

BIN="${1:-}"
if [ -z "$BIN" ]; then
  for c in ./lorahub-linux-arm64 ./lorahub ../lorahub-linux-arm64 ./dist/lorahub-linux-arm64; do
    [ -x "$c" ] && BIN="$c" && break
  done
fi
[ -n "$BIN" ] && [ -x "$BIN" ] || { echo "lorahub binary not found (pass it as first argument)"; exit 1; }
[ "$(id -u)" = 0 ] || { echo "run as root"; exit 1; }
HERE="$(cd "$(dirname "$0")" && pwd)"

echo ">> Stopping legacy services that use ports 80 / 5555 (bridge + Flask UI)"
for s in bobcat-lora-bridge bobcat-web; do
  if systemctl list-unit-files | grep -q "^$s.service"; then
    systemctl disable --now "$s" || true
  fi
done

echo ">> Installing binary"
install -m 0755 "$BIN" /usr/local/bin/lorahub

echo ">> Installing configuration"
mkdir -p /etc/lorahub /var/lib/lorahub
[ -f /etc/lorahub/lorahub.yml ] || install -m 0644 "$HERE/lorahub.yml" /etc/lorahub/lorahub.yml

if [ -f /etc/lora/config.json ]; then
  echo ">> Checking raw_lora_app ports (port_up must be 5555, port_down 5556)"
  grep -q '"port_up": *5555' /etc/lora/config.json || echo "   WARNING: set \"port_up\": 5555 in /etc/lora/config.json"
  grep -q '"port_down": *5556' /etc/lora/config.json || echo "   WARNING: set \"port_down\": 5556 in /etc/lora/config.json"
fi

echo ">> Installing systemd service"
install -m 0644 "$HERE/lorahub.service" /etc/systemd/system/lorahub.service
systemctl daemon-reload
systemctl enable --now lorahub
sleep 1
systemctl --no-pager status lorahub | head -n 5 || true
IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
echo
echo "LoRaHub is running. Open http://${IP:-<bobcat-ip>}/ to create the admin account."
