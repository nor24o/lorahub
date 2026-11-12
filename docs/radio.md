# Radio

What the LoRaHub gateways (SX1302: Bobcat Miner 300, Dragino LPS8) listen on
and how they answer, so a node built with other settings still gets through.

## What the gateways hear

| | |
|---|---|
| Channels | 867.1, 867.3, 867.5, 867.7, 867.9, 868.1, 868.3, 868.5 MHz |
| Bandwidth | 125 kHz |
| Spreading factor | SF7 to SF12, all at once on every channel |
| Sync word | private (`0x12` on an SX1276, `0x1424` on an SX1262; RadioLib takes `0x12` for both) |
| IQ | normal (uplinks are never inverted) |
| Header and CRC | explicit header, CRC on |
| Preamble | 8 symbols |

Every gateway that hears a packet forwards it; LoRaHub merges them into one
message with the signal of each.

## How downlinks come back

| Node | When | Frequency and SF |
|---|---|---|
| Class A | 1 s after the end of its uplink (`RX_DELAY_MS`), through the gateway that heard it best | its radio block, else the uplink's |
| Class C | at once, through the gateway that heard it best last | its radio block (default 869.525 MHz SF9) |

869.525 MHz is the band with 10 % duty cycle and up to 500 mW, the usual one
for answers in EU868. It is outside the gateways' receive channels, so they do
not hear their own answers there.

With `RX_INVERT_IQ=1` LoRaHub sends with inverted IQ: nothing but the node
hears it as a packet. With normal IQ, other gateways may hear a downlink, and
LoRaHub recognises and ignores its own transmissions for 20 s.

## Airtime and duty cycle

Time on air of a 20-byte report (125 kHz, 4/5):

| SF | Airtime | Shortest interval at 1 % |
|---|---|---|
| 7 | 57 ms | 6 s |
| 8 | 103 ms | 11 s |
| 9 | 185 ms | 19 s |
| 10 | 371 ms | 38 s |
| 11 | 741 ms | 75 s |
| 12 | 1319 ms | 132 s |

EU868 allows 1 % on 867-868.6 MHz: 36 s per hour. The firmware counts its own
airtime per hour and skips a packet that would go over. Largest packet: 51
bytes at SF10-SF12, 115 at SF9, 222 at SF7-SF8; a longer description is split
into parts automatically.

Choose the lowest SF that reaches reliably: watch the node's RSSI and SNR on
its LoRaHub page. SNR above about -7 dB at SF9 leaves room to go faster.
