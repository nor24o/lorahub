#!/usr/bin/env python3
"""FSK devices through LoRaHub's FSK bridge: receive what they send (uplink)
and send to them (downlink). Python 3, standard library only.

LoRaHub, FSK bridge page:
  Bridge on                   ticked
  Send every packet heard to  <this PC's IP>:1731   (this script prints it)
  Take packets to send on     :1730
  Format                      JSON

Uplink: every FSK packet the gateways hear arrives here as a JSON datagram.
Downlink: a JSON datagram {"data_hex": ...} to the hub's port 1730 goes on the
air as FSK at once, through the gateway that heard the device last (or the
one named). The gateway adds the preamble, sync word, length byte and CRC.

  python fsk_client.py listen                     print every packet heard
  python fsk_client.py listen --reply AA5501      ... and answer each one at once
  python fsk_client.py listen --log fsk.csv       ... and append them to a CSV file
  python fsk_client.py send AA5501                send one packet
  python fsk_client.py send AA5501 --gateway eui-0016c001ff1316e0 --power 10
  python fsk_client.py --raw send AA5501          the bridge's format is Raw (bytes only)

Your own processing goes in on_packet() below.
"""
import argparse
import csv
import datetime
import json
import socket
import sys

HUB = "192.168.1.15"  # the LoRaHub Bobcat
HUB_PORT = 1730       # FSK bridge "Take packets to send on"
MY_PORT = 1731        # FSK bridge "Send every packet heard to" = this PC's IP and this port


def on_packet(data: bytes, pkt: dict):
    """Called for every FSK packet heard. data is what the device sent (without
    length byte and CRC); pkt is the whole message from LoRaHub. Return bytes to
    answer the device at once, or None."""
    return None


def send(sock, data: bytes, hub, raw=False, **radio):
    """Send data to the device as FSK. radio may hold gateway, frequency (MHz),
    bit_rate, deviation (kHz), power (dBm) and preamble (bytes); anything left
    out takes the FSK bridge settings. With the bridge set to Raw, the datagram
    is the bytes alone and the bridge settings apply."""
    if raw:
        sock.sendto(data, hub)
        return
    msg = {"data_hex": data.hex().upper()}
    msg.update({k: v for k, v in radio.items() if v is not None})
    sock.sendto(json.dumps(msg).encode(), hub)


def my_ip(hub_host):
    """The address of this PC as the hub sees it."""
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        s.connect((hub_host, 9))
        return s.getsockname()[0]
    except OSError:
        return "this-pc"
    finally:
        s.close()


def describe(pkt: dict) -> str:
    if "frequency" not in pkt:
        return f"{pkt.get('size', '?')} bytes  (Raw format: no radio details)"
    gws =", ".join(f"{g.get('gateway_id', '?')} {g.get('rssi', 0):.0f} dBm" for g in pkt.get("gateways", []))
    return f"{pkt.get('size', '?')} bytes  {pkt.get('frequency', 0):.3f} MHz  {pkt.get('bit_rate', '?')} bit/s  [{gws}]"


def listen(args):
    hub = (args.hub, args.hub_port)
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    try:
        sock.bind(("", args.port))
    except OSError as e:
        sys.exit(f"cannot listen on UDP port {args.port}: {e}")
    print(f"Listening on UDP {args.port}. In LoRaHub's FSK bridge set 'Send every packet heard to' = {my_ip(args.hub)}:{args.port}")
    print("(On Windows, allow Python through the firewall for incoming UDP.)  Ctrl+C stops.\n", flush=True)
    reply = bytes.fromhex(args.reply) if args.reply else None
    log = None
    if args.log:
        new = not _exists(args.log)
        log_file = open(args.log, "a", newline="")
        log = csv.writer(log_file)
        if new:
            log.writerow(["time", "data_hex", "size", "frequency_mhz", "bit_rate", "gateways", "best_rssi"])
    try:
        while True:
            raw, _ = sock.recvfrom(4096)
            now = datetime.datetime.now().strftime("%H:%M:%S.%f")[:-3]
            raw_format = args.raw
            try:
                pkt = json.loads(raw)
                data = bytes.fromhex(pkt["data_hex"])
            except (ValueError, KeyError, TypeError):
                # The bridge is set to Raw: the datagram is the packet itself, and so is the answer.
                pkt, data, raw_format = {"data_hex": raw.hex().upper(), "size": len(raw), "gateways": []}, raw, True
            print(f"{now}  UP    {data.hex(' ').upper()}   {describe(pkt)}", flush=True)
            if log:
                gws = pkt.get("gateways", [])
                log.writerow([now, data.hex().upper(), len(data), pkt.get("frequency"), pkt.get("bit_rate"),
                              " ".join(g.get("gateway_id", "") for g in gws), max((g.get("rssi", -999) for g in gws), default="")])
                log_file.flush()
            answer = on_packet(data, pkt)
            if answer is None:
                answer = reply
            if answer:
                send(sock, answer, hub, raw=raw_format)  # at once, while the device still listens
                print(f"{now}  DOWN  {answer.hex(' ').upper()}", flush=True)
    except KeyboardInterrupt:
        print("stopped")


def send_once(args):
    try:
        data = bytes.fromhex(args.hex.replace(":", ""))
    except ValueError:
        sys.exit(f"not hex: {args.hex}")
    if not 1 <= len(data) <= 255:
        sys.exit("a packet has 1 to 255 bytes")
    sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    send(sock, data, (args.hub, args.hub_port), raw=args.raw, gateway=args.gateway, frequency=args.frequency, bit_rate=args.bit_rate,
         deviation=args.deviation, power=args.power, preamble=args.preamble)
    print(f"sent {data.hex(' ').upper()} to {args.hub}:{args.hub_port}; check LoRaHub, FSK bridge page, FSK traffic")


def _exists(path):
    try:
        open(path).close()
        return True
    except OSError:
        return False


def main():
    p = argparse.ArgumentParser(description="FSK devices through LoRaHub's FSK bridge")
    p.add_argument("--hub", default=HUB, help=f"LoRaHub address (default {HUB})")
    p.add_argument("--hub-port", type=int, default=HUB_PORT, help=f"FSK bridge port on the hub (default {HUB_PORT})")
    p.add_argument("--raw", action="store_true", help="the FSK bridge's format is Raw: send the bytes alone")
    sub = p.add_subparsers(dest="cmd", required=True)

    pl = sub.add_parser("listen", help="print every FSK packet heard (uplink)")
    pl.add_argument("--port", type=int, default=MY_PORT, help=f"UDP port to listen on (default {MY_PORT})")
    pl.add_argument("--reply", help="hex bytes to send back after every packet heard")
    pl.add_argument("--log", help="append every packet to this CSV file")

    ps = sub.add_parser("send", help="send one FSK packet (downlink)")
    ps.add_argument("hex", help="the bytes, e.g. AA5501 or 'AA 55 01'")
    ps.add_argument("--gateway", help="gateway ID; default: the one that heard the device last")
    ps.add_argument("--frequency", type=float, help="MHz")
    ps.add_argument("--bit-rate", type=int, help="bit/s")
    ps.add_argument("--deviation", type=int, help="kHz")
    ps.add_argument("--power", type=int, help="dBm")
    ps.add_argument("--preamble", type=int, help="bytes")

    args = p.parse_args()
    if args.cmd == "send":
        args.hex = args.hex.replace(" ", "")
        send_once(args)
    else:
        listen(args)


if __name__ == "__main__":
    main()
