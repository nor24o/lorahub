# Adding a Bobcat as a satellite

A satellite Bobcat runs only the radio engine (`raw_lora_app`) and a WireGuard
tunnel to the LoRaHub Bobcat. Its packets are merged, decoded and published by
the hub, and the hub can send downlinks through it. It does not run LoRaHub.

## Before you start
1. Install the same Armbian on the new Bobcat as on the hub (bookworm with the
   `rk3566-bobcat-29x` device tree, so `/dev/spidev5.0` exists), or copy the
   hub's whole eMMC onto it (see below).
2. Connect it to the network and allow root to log in with your SSH key:
   `ssh-copy-id root@<new bobcat ip>` (a copy of the hub already allows it).
3. Do not run the `install.sh` of the old firmware branch, and do not install
   LoRaHub on it.

## A copy of the hub's eMMC
A copy starts as a second hub with the same identity: LoRaHub, Tailscale and
the tunnel all come up with the hub's keys, and both then fight over the
Home Assistant broker and the tunnel address. Switch the copy on only long
enough to run this installer. The installer recognises the copy (same
machine-id or the hub's tunnel key) and first:

- stops and disables LoRaHub and Tailscale there;
- moves the hub's data (`/var/lib/lorahub`), Tailscale login and tunnel key to
  `/root/hub-copy` (nothing is deleted; remove that folder once all is well);
- gives it a new machine-id, new SSH host keys and the host name
  `bobcat-sat<N>` (`--name` picks another).

Do not run `tailscale logout` on the copy: it shares the hub's login, so that
would sign the hub out.

## Run
From the PC, in Git Bash, inside the `lorahub` folder:

```
deploy/satellite/add-satellite.sh <new bobcat ip> --check
deploy/satellite/add-satellite.sh <new bobcat ip>
```

The first command only checks both Bobcats. The second one:

1. installs `wireguard-tools` and `dnsmasq-base` (for the Wi-Fi access point with its own network) on the new Bobcat and creates its tunnel key;
2. copies the engine, `reset_lgw.sh` and the `raw-lora` service from the hub;
3. copies the hub's radio settings, pointed at the hub's tunnel address, without
   the hub's link key and without the old bridge's MQTT and UDP sections;
4. gives it the next free tunnel address (`10.77.0.3` for the first one) and
   adds it to the hub's tunnel without disturbing the other gateways;
5. starts the radio, checks it listens, and waits for the hub to pair it.

The new gateway then appears under **Gateways** in the console.

## The satellite page
The installer also puts a small web page on the satellite, `http://<satellite ip>/`
(program `lorahub-sat`, service `lorahub-sat`; build it first with `make arm64`).
Sign in with the Bobcat's root password. The hub's Gateways page links to it
("Device page").

- **Status:** radio engine (state, packets, CRC errors, chip temperature, link to
  the hub), tunnel, board (CPU temperature, load, memory, disk) and interfaces.
- **Network:** wired and Wi-Fi address settings (DHCP or fixed, DNS), Wi-Fi
  client (scan, connect, saved networks, radio on or off), and an **access
  point** in one of two modes: bridged to the cable (phones are on the home
  network and get addresses from the home router) or with its own network
  10.42.0.x (the board hands out addresses and shares its cable; without a cable
  it is a private network for setting the board up in the field, page at
  http://10.42.0.1). With "only while the cable is connected" the Wi-Fi goes
  back to being a normal client when the cable is unplugged.
- **Tunnel:** the hub's home and internet addresses.
- **General:** host name, time zone, time from the internet.
- **SD card:** mount a card in the reader (read-only or for writing), browse
  and download its files, unmount it.
- **Lights:** normal, steady (lit all the time), quiet (no packet flashes),
  solid (lit, no flashes), errors only or off; a colour for the idle light, for
  packets received and for packets sent, each with a 5-second "Try"; and an
  optional night schedule (colours need raw_lora_app 2.2).
- **System and logs:** restart the radio engine, reboot, power off, journals.

The hub has the same pages in its console under **Device**.

Every network or tunnel change is checked: for 60 s the satellite pings the hub
through the tunnel, and if the hub does not answer the previous setting comes
back by itself. Radio settings stay on the hub's Remote config page.

Options: `--hub HOST` (default `192.168.1.15`), `--tunnel-ip IP`, `--name NAME`, and
`--wan HOST:PORT` for a satellite that should also connect from another network
(forward that UDP port to the hub on the router). Running it again is safe;
files it replaces are kept as `*.before-satellite`.
