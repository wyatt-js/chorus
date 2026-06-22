# AirPlay 2 → Samsung Neo QLED: root-cause findings

Captured a **known-good Apple session** (macOS native AirPlay → the same TV) with
`tcpdump -i en0 -s0 -w airplay2.pcap ether host 4c:57:39:4b:c4:d2` while audio
played audibly. Comparing it to what chorus/airplayrelay sends:

## What Apple's working sender does
- **IPv6 for everything** (6434 IPv6 vs 935 IPv4 pkts). Control, audio, and PTP
  all run over the TV's routable IPv6 (`2601:…:4b33:ff45`).
- **Audio = realtime RTP, stream type 96, UDP.** RTP header `80 60 …`
  (v2, payload type 0x60 = 96), SSRC=0. Mac:57766 → TV:51392.
- **125.35 pkts/sec** = 44100 / 352 → 352 samples/packet, ~500 B/packet.
- Control: RTSP over **TCP port 7000** (IPv6).
- Timing: **PTP** on 319/320 (IPv6); TV is PTP **master**, Mac is **slave**
  (Mac sends Delay_Req, TV sends Announce) — chorus already does this correctly.

## What chorus does (the mismatch)
- **IPv4** (connects to the TV's IPv4).
- **Audio = buffered, stream type 103, TCP** (forced because the TV *advertises*
  buffered support — but Apple doesn't use it here).
- The TV **accepts** chorus's type-103 SETUP/RECORD/SETRATEANCHORTIME (all 200)
  and shows the now-playing UI, but **renders no audio** — i.e. it tolerates the
  buffered setup but only actually plays the realtime (type 96) path Apple uses.

## Conclusion / fix direction
Two differences vs the known-good path, in likely-priority order:

1. **Use realtime audio (type 96, UDP)** instead of buffered (103, TCP).
   **DONE** — `stream_type` forced to 96 in `connection/manager.rs`. Verified at
   the protocol level against the TV: SETUP #2 → 200, SETRATEANCHORTIME →
   accepted (the TV honors it for type 96 too), RECORD → accepted, audio now over
   **UDP** ("Connecting Audio to …", no TCP), 1000+ packets, session stable, no
   teardown. **Audible output not yet confirmed (needs a listen test).**

2. **Use IPv6** to reach the TV — NOT yet done. Apple uses IPv6 exclusively; the
   TV's realtime renderer may require the audio/RTSP to arrive over IPv6. The
   crate currently dials the TV's IPv4 (`device.address()`). This is the next
   lever if type-96-over-IPv4 is still silent. Larger change: resolve + use the
   TV's IPv6 and bind v6 sockets through the SETUP/audio/control/PTP paths.

Final confirmation requires listening on the TV (protocol acceptance ≠ audible).
Test order: listen with #1 (current build). If silent, do #2 and listen again.
