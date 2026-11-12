#!/usr/bin/env bash
# Turns another Bobcat Miner 300 into a satellite of the LoRaHub Bobcat. The
# satellite gets only the radio engine (raw_lora_app) and a WireGuard tunnel
# to the hub; decoding, MQTT and Home Assistant stay on the hub.
#
# Run it from a computer that logs in as root to both Bobcats with an SSH key
# (Git Bash on Windows works):
#
#   deploy/satellite/add-satellite.sh 192.168.1.20
#
#   --hub HOST       the LoRaHub Bobcat on the home network (default 192.168.1.15)
#   --tunnel-ip IP   the satellite's tunnel address (default: the next free one)
#   --wan HOST:PORT  the hub's internet address and WireGuard port, for a
#                    satellite that will also work away from home
#   --name NAME      the satellite's host name (a copy of the hub gets
#                    bobcat-sat<N>, N being the end of its tunnel address)
#   --check          only check both Bobcats and say what would happen
#
# A Bobcat whose eMMC was copied from the hub is detected and given its own
# identity first: LoRaHub and Tailscale are stopped there, the hub's data, keys
# and Tailscale login are moved to /root/hub-copy, and it gets a new machine-id,
# SSH host keys, host name and tunnel key.
#
# Running it again is safe: keys, the tunnel address and the hub's entry for
# the satellite are reused, and files it replaces are kept as *.before-satellite.
set -euo pipefail

HUB=192.168.1.15
NEW=""
TUN=""
WAN=""
NEWNAME=""
CHECK=0
while [ $# -gt 0 ]; do
    case "$1" in
        --hub) HUB="$2"; shift 2 ;;
        --tunnel-ip) TUN="$2"; shift 2 ;;
        --wan) WAN="$2"; shift 2 ;;
        --name) NEWNAME="$2"; shift 2 ;;
        --check) CHECK=1; shift ;;
        -h|--help) sed -n '2,25p' "$0"; exit 0 ;;
        -*) echo "unknown option $1" >&2; exit 2 ;;
        *) NEW="$1"; shift ;;
    esac
done
[ -n "$NEW" ] || { sed -n '2,25p' "$0"; exit 2; }

SSH=(ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=accept-new)
hub() { "${SSH[@]}" "root@$HUB" "$@"; }
sat() { "${SSH[@]}" "root@$NEW" "$@"; }
say() { printf '\n== %s\n' "$*"; }
ok() { printf '   ok: %s\n' "$*"; }
die() { printf '\nSTOP: %s\n' "$*" >&2; exit 1; }

# ---------- checks (nothing is changed here) ----------

say "Checking the hub $HUB"
hub true 2>/dev/null || die "cannot reach root@$HUB or log in with your SSH key (is it switched on and on the network?)"
hub 'systemctl is-active --quiet lorahub' || die "LoRaHub is not running on $HUB"
hub 'systemctl is-active --quiet wg-quick@wg0' || die "WireGuard (wg-quick@wg0) is not running on $HUB"
hub 'test -x /usr/local/bin/raw_lora_app && test -x /usr/local/bin/reset_lgw.sh && test -f /etc/lora/config.json' \
    || die "the radio engine files are missing on $HUB"
HUB_PUB=$(hub 'wg show wg0 public-key')
HUB_PORT=$(hub 'wg show wg0 listen-port')
HUB_TUN=$(hub "ip -4 -o addr show wg0 | awk '{print \$4}' | cut -d/ -f1")
NET=${HUB_TUN%.*}
HUB_DTB=$(hub "grep -h '^fdtfile=' /boot/armbianEnv.txt 2>/dev/null || true")
ok "LoRaHub and WireGuard run; tunnel $HUB_TUN, UDP $HUB_PORT"

say "Checking the new Bobcat $NEW"
sat true 2>/dev/null || die "cannot reach root@$NEW or log in with your SSH key (is it on the network? copy the key with: ssh-copy-id root@$NEW)"
# A copied eMMC has the hub's machine-id, but every boot has its own boot_id.
[ "$(sat 'cat /proc/sys/kernel/random/boot_id')" != "$(hub 'cat /proc/sys/kernel/random/boot_id')" ] || die "$NEW is the hub itself"
[ "$(sat 'uname -m')" = aarch64 ] || die "$NEW is not a 64-bit ARM system"
sat 'test -e /dev/spidev5.0' || die "$NEW has no /dev/spidev5.0, so the radio cannot be reached.
      Install the same Armbian as the hub (its device tree: ${HUB_DTB:-unknown})."
NEW_DTB=$(sat "grep -h '^fdtfile=' /boot/armbianEnv.txt 2>/dev/null || true")
[ "$NEW_DTB" = "$HUB_DTB" ] || printf '   note: device tree differs (%s here, %s on the hub)\n' "${NEW_DTB:-none}" "${HUB_DTB:-none}"
# A copy of the hub's eMMC: same machine-id or the hub's own tunnel key.
CLONE=0
SAT_PUB=$(sat 'test -s /etc/wireguard/wg0.key && wg pubkey < /etc/wireguard/wg0.key 2>/dev/null || true')
if [ "$(sat 'cat /etc/machine-id')" = "$(hub 'cat /etc/machine-id')" ] || [ "$SAT_PUB" = "$HUB_PUB" ]; then
    CLONE=1
    SAT_PUB=""
    echo "   note: $NEW is a copy of the hub; it will get its own identity first"
    echo "         (LoRaHub and Tailscale stopped there, hub data and keys moved to /root/hub-copy)"
elif sat 'systemctl is-active --quiet lorahub 2>/dev/null'; then
    die "LoRaHub runs on $NEW. A satellite must not run it; stop it first: systemctl disable --now lorahub"
fi
OLD=$(sat "systemctl list-units --type=service --state=running --no-legend 2>/dev/null | grep -iE 'bridge|lora[-_]web|mosquitto' | awk '{print \$1}' | tr '\n' ' ' || true")
[ -z "$OLD" ] || printf '   note: these old services still run there and are left alone: %s\n' "$OLD"
NAME=$(sat 'hostname')
ok "$NAME: aarch64, radio SPI present"

if [ -z "$TUN" ] && [ -n "$SAT_PUB" ]; then
    TUN=$(hub 'wg show wg0 allowed-ips' | awk -v k="$SAT_PUB" '$1 == k {print $2}' | cut -d/ -f1 | head -n 1)
fi
if [ -z "$TUN" ]; then
    USED=$(hub 'wg show wg0 allowed-ips' | grep -oE "$NET\.[0-9]+" || true)
    for n in $(seq 3 254); do
        echo "$USED" | grep -qx "$NET.$n" || { TUN="$NET.$n"; break; }
    done
fi
[ -n "$TUN" ] || die "no free tunnel address left in $NET.0/24"
if [ -z "$NEWNAME" ] && { [ "$CLONE" = 1 ] || [ "$NAME" = "$(hub hostname)" ]; }; then
    NEWNAME="bobcat-sat${TUN##*.}"
fi
[ "$NEWNAME" = "$NAME" ] && NEWNAME=""
ok "tunnel address for $NAME: $TUN${NEWNAME:+, new host name $NEWNAME}"

if [ "$CHECK" = 1 ]; then
    say "Check only: nothing was changed"
    [ "$CLONE" = 0 ] || echo "   Would first give $NEW its own identity (it is a copy of the hub)."
    echo "   Would install the radio engine from $HUB on $NEW (${NEWNAME:-$NAME}), with the tunnel at $TUN"
    echo "   and the hub reached at $HUB:$HUB_PORT${WAN:+, or $WAN away from home}."
    exit 0
fi

# ---------- install ----------

if [ "$CLONE" = 1 ]; then
    say "Giving the copy of the hub its own identity"
    # Nothing is deleted: the hub's data, tunnel key and Tailscale login go to
    # /root/hub-copy (an existing copy there is kept as *.~N~).
    sat 'systemctl disable --now lorahub tailscaled >/dev/null 2>&1 || true
         systemctl disable --now wg-quick@wg0 raw-lora >/dev/null 2>&1 || true
         mkdir -p /root/hub-copy && chmod 700 /root/hub-copy
         for p in /var/lib/lorahub /var/lib/tailscale/tailscaled.state /etc/wireguard/wg0.key /etc/wireguard/wg0.conf; do
             [ -e "$p" ] && mv --backup=numbered "$p" /root/hub-copy/; true
         done
         rm -f /etc/machine-id /var/lib/NetworkManager/secret_key
         systemd-machine-id-setup >/dev/null'
    [ "$(sat 'cat /etc/machine-id')" != "$(hub 'cat /etc/machine-id')" ] || die "the machine-id on $NEW did not change"
    ok "LoRaHub and Tailscale stopped, hub data and keys in /root/hub-copy, new machine-id"
    # New SSH host keys, so the two Bobcats can be told apart; this PC then
    # learns the satellite's new key.
    sat 'rm -f /etc/ssh/ssh_host_*_key /etc/ssh/ssh_host_*_key.pub && ssh-keygen -A >/dev/null && systemctl restart ssh'
    ssh-keygen -R "$NEW" >/dev/null 2>&1 || true
    sat true || die "cannot log in to $NEW after its SSH host keys changed"
    ok "new SSH host keys"
fi
if [ -n "$NEWNAME" ]; then
    sat "hostnamectl set-hostname '$NEWNAME'
         if grep -q '^127\.0\.1\.1' /etc/hosts; then sed -i 's/^127\.0\.1\.1[[:space:]].*/127.0.1.1\t$NEWNAME/' /etc/hosts
         else printf '127.0.1.1\t%s\n' '$NEWNAME' >> /etc/hosts; fi
         systemctl try-restart avahi-daemon >/dev/null 2>&1 || true"
    ok "host name $NAME -> $NEWNAME"
    NAME=$NEWNAME
fi

say "Installing WireGuard on $NAME"
sat 'pk=""; command -v wg >/dev/null || pk="wireguard-tools"; [ -x /usr/sbin/dnsmasq ] || pk="$pk dnsmasq-base"; [ -z "$pk" ] || { apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq $pk; }'
SAT_PUB=$(sat 'umask 077; mkdir -p /etc/wireguard; test -s /etc/wireguard/wg0.key || wg genkey > /etc/wireguard/wg0.key; wg pubkey < /etc/wireguard/wg0.key')
[ "$SAT_PUB" != "$HUB_PUB" ] || die "$NEW has the hub's tunnel key; move /etc/wireguard/wg0.key away there and run again"
ok "key ready"

say "Copying the radio engine from the hub"
sat 'systemctl stop raw-lora 2>/dev/null || true
     for f in /usr/local/bin/raw_lora_app /usr/local/bin/reset_lgw.sh /etc/lora/config.json /etc/systemd/system/raw-lora.service; do
         [ -e "$f" ] && [ ! -e "$f.before-satellite" ] && cp -p "$f" "$f.before-satellite"; true
     done
     mkdir -p /etc/lora'
UNIT=$(hub 'systemctl show -p FragmentPath --value raw-lora')
copy() { hub "cat '$1'" | sat "cat > '$2.new' && chmod $3 '$2.new' && mv '$2.new' '$2'"; }
copy /usr/local/bin/raw_lora_app /usr/local/bin/raw_lora_app 755
copy /usr/local/bin/reset_lgw.sh /usr/local/bin/reset_lgw.sh 755
copy "$UNIT" /etc/systemd/system/raw-lora.service 644
if sat 'ldd /usr/local/bin/raw_lora_app | grep -q "not found"'; then
    die "the engine is missing system libraries on $NEW: $(sat 'ldd /usr/local/bin/raw_lora_app | grep "not found"')"
fi
ok "engine, reset script and service copied"

# The hub's radio settings, pointed at the hub through the tunnel. Its link key
# stays behind (the hub pairs the satellite with a fresh one), and so do the
# leftovers of the old bridge, which hold a broker login and a UDP stream.
hub "python3 - '$HUB_TUN'" <<'PY' | sat 'cat > /etc/lora/config.json.new && chmod 644 /etc/lora/config.json.new && mv /etc/lora/config.json.new /etc/lora/config.json'
import json, sys
cfg = json.load(open('/etc/lora/config.json'))
for key in ('link_key', 'mqtt', 'udp_stream', 'device_aliases'):
    cfg.pop(key, None)
cfg['target_ip'] = sys.argv[1]
cfg['full_duplex'] = False
json.dump(cfg, sys.stdout, indent=4)
PY
ok "radio settings copied, sending to $HUB_TUN"

say "Setting up the tunnel"
sat "cat > /etc/wireguard/wg0.conf <<EOF
# Tunnel to the LoRaHub Bobcat; written by add-satellite.sh
[Interface]
Address = $TUN/24
PostUp = wg set %i private-key /etc/wireguard/%i.key

[Peer]
# LoRaHub Bobcat
PublicKey = $HUB_PUB
Endpoint = $HUB:$HUB_PORT
AllowedIPs = $NET.0/24
PersistentKeepalive = 25
EOF
chmod 600 /etc/wireguard/wg0.conf"
if ! hub "grep -qF '$SAT_PUB' /etc/wireguard/wg0.conf"; then
    hub "printf '\n[Peer]\n# Bobcat satellite %s\nPublicKey = %s\nAllowedIPs = %s/32\n' '$NAME' '$SAT_PUB' '$TUN' >> /etc/wireguard/wg0.conf"
fi
hub "wg set wg0 peer '$SAT_PUB' allowed-ips '$TUN/32'"   # live, so the hub's other gateways stay connected
ok "hub knows $NAME as $TUN"

# Away from home the satellite tries the hub's internet address too.
sat "cat > /etc/wireguard/roam.env <<EOF
PEER='$HUB_PUB'
LAN='$HUB:$HUB_PORT'
WAN='$WAN'
EOF
cat > /usr/local/bin/wg_roam.sh <<'EOF'
#!/bin/sh
# While the tunnel has no recent handshake, alternate between the hub's
# home-network address and its internet address (both in roam.env).
. /etc/wireguard/roam.env
[ -n \"\$WAN\" ] || exit 0
now=\$(date +%s)
last=\$(wg show wg0 latest-handshakes 2>/dev/null | awk '{print \$2}')
[ -n \"\$last\" ] && [ \"\$last\" -gt 0 ] && [ \$((now - last)) -lt 180 ] && exit 0
cur=\$(wg show wg0 endpoints 2>/dev/null | awk '{print \$2}')
if [ \"\$cur\" = \"\$LAN\" ]; then next=\$WAN; else next=\$LAN; fi
wg set wg0 peer \"\$PEER\" endpoint \"\$next\"
EOF
chmod 755 /usr/local/bin/wg_roam.sh
cat > /etc/systemd/system/wg-roam.service <<'EOF'
[Unit]
Description=Keep the tunnel to the LoRaHub Bobcat up from any network
[Service]
Type=oneshot
ExecStart=/usr/local/bin/wg_roam.sh
EOF
cat > /etc/systemd/system/wg-roam.timer <<'EOF'
[Unit]
Description=Check the tunnel to the LoRaHub Bobcat every minute
[Timer]
OnBootSec=1min
OnUnitActiveSec=1min
[Install]
WantedBy=timers.target
EOF
systemctl daemon-reload
systemctl enable --now wg-quick@wg0 >/dev/null 2>&1
systemctl restart wg-quick@wg0
systemctl enable --now wg-roam.timer >/dev/null 2>&1"
for i in $(seq 1 10); do
    sat "ping -c 1 -W 2 $HUB_TUN >/dev/null 2>&1" && break
    [ "$i" = 10 ] && die "the tunnel to $HUB_TUN did not come up"
    sleep 2
done
ok "tunnel up: $NAME reaches the hub at $HUB_TUN"

say "Starting the radio engine"
sat 'systemctl enable raw-lora >/dev/null 2>&1; systemctl restart raw-lora'
listening=0
for i in $(seq 1 15); do
    sleep 2
    if sat "python3 -c \"
import json, socket, sys
c = socket.create_connection(('127.0.0.1', 5557), 2)
b = b''
while True:
    p = c.recv(65536)
    if not p:
        break
    b += p
sys.exit(0 if json.loads(b).get('rx_status') == 'LISTENING' else 1)\"" 2>/dev/null; then
        listening=1
        break
    fi
done
if [ "$listening" != 1 ]; then
    sat 'journalctl -u raw-lora -n 15 --no-pager -o cat' || true
    die "the radio did not start listening on $NAME (log above)"
fi
ok "radio listening"

say "Waiting for the hub to pair it"
gw=""
for i in $(seq 1 30); do
    gw=$(hub "python3 - '$TUN'" <<'PY' || true
import json, sqlite3, sys
db = sqlite3.connect('file:/var/lib/lorahub/lorahub.db?mode=ro', uri=True)
for (data,) in db.execute('select data from gateways'):
    g = json.loads(data)
    if g.get('last_addr') == sys.argv[1]:
        status = g.get('last_status') or {}
        if isinstance(status, str):
            status = json.loads(status)
        print(g['gateway_id'], 'paired' if status.get('auth') else 'not paired yet')
PY
)
    case "$gw" in *" paired") break ;; esac
    sleep 3
done
case "$gw" in
    *" paired") ok "$gw" ;;
    "") die "the hub has not heard from $NAME yet; check Gateways in the console" ;;
    *) printf '   note: %s (it pairs on its own within a minute)\n' "$gw" ;;
esac

say "Installing the satellite page"
HERE=$(cd "$(dirname "$0")" && pwd)
PAGE_BIN="$HERE/../../dist/lorahub-sat-linux-arm64"
if [ -f "$PAGE_BIN" ]; then
    sat 'cat > /usr/local/bin/lorahub-sat.new' < "$PAGE_BIN"
    sat 'cat > /etc/systemd/system/lorahub-sat.service' < "$HERE/lorahub-sat.service"
    sat 'chmod 755 /usr/local/bin/lorahub-sat.new && mv /usr/local/bin/lorahub-sat.new /usr/local/bin/lorahub-sat
         systemctl daemon-reload; systemctl enable lorahub-sat >/dev/null 2>&1; systemctl restart lorahub-sat'
    ok "http://$NEW/ (sign in with the Bobcat's root password)"
else
    printf '   note: %s is missing; build it with "make arm64" and run this again\n' "dist/lorahub-sat-linux-arm64"
fi

say "Done"
echo "   $NAME ($NEW) is a satellite of $HUB, tunnel $TUN."
echo "   Its page: http://$NEW/ (telemetry, wired, Wi-Fi, access point, tunnel)."
echo "   It appears under Gateways in the console; give it a name there."
[ -n "$WAN" ] || echo "   To use it away from home, run again with --wan <hub internet address>:$HUB_PORT and forward that UDP port to $HUB."
