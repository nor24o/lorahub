# Adding a Bobcat as a satellite

A satellite Bobcat runs only the radio engine (`raw_lora_app`) and a WireGuard
tunnel to the LoRaHub Bobcat. Its packets are merged, decoded and published by
the hub, and the hub can send downlinks through it. It does not run LoRaHub.

## Before you start
1. Install the same Armbian on the new Bobcat as on the hub (bookworm with the
   `rk3566-bobcat-29x` device tree, so `/dev/spidev5.0` exists).
2. Connect it to the network and allow root to log in with your SSH key:
   `ssh-copy-id root@<new bobcat ip>`.
3. Do not run the `install.sh` of the old firmware branch, and do not install
   LoRaHub on it.

## Run
From the PC, in Git Bash, inside the `lorahub` folder:

```
deploy/satellite/add-satellite.sh <new bobcat ip> --check
deploy/satellite/add-satellite.sh <new bobcat ip>
```

The first command only checks both Bobcats. The second one:

1. installs `wireguard-tools` on the new Bobcat and creates its key;
2. copies the engine, `reset_lgw.sh` and the `raw-lora` service from the hub;
3. copies the hub's radio settings, pointed at the hub's tunnel address, without
   the hub's link key and without the old bridge's MQTT and UDP sections;
4. gives it the next free tunnel address (`10.77.0.3` for the first one) and
   adds it to the hub's tunnel without disturbing the other gateways;
5. starts the radio, checks it listens, and waits for the hub to pair it.

The new gateway then appears under **Gateways** in the console.

Options: `--hub HOST` (default `192.168.1.15`), `--tunnel-ip IP`, and
`--wan HOST:PORT` for a satellite that should also connect from another network
(forward that UDP port to the hub on the router). Running it again is safe;
files it replaces are kept as `*.before-satellite`.
