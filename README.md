<div align="center">

```
            ██╗  ██╗██╗   ██╗██████╗ ██████╗  █████╗
            ██║  ██║╚██╗ ██╔╝██╔══██╗██╔══██╗██╔══██╗
            ███████║ ╚████╔╝ ██║  ██║██████╔╝███████║
            ██╔══██║  ╚██╔╝  ██║  ██║██╔══██╗██╔══██║
            ██║  ██║   ██║   ██████╔╝██║  ██║██║  ██║
            ╚═╝  ╚═╝   ╚═╝   ╚═════╝ ╚═╝  ╚═╝╚═╝  ╚═╝
```

### One stream in, every platform out.

A self-hosted RTMP relay and live transcoder. Send one feed from OBS,
fan it out to Twitch, YouTube, TikTok and any RTMP target at once,
each with its own rendition, from a single decode.

[![CI](https://github.com/Elchi-dev/hydra/actions/workflows/ci.yml/badge.svg)](https://github.com/Elchi-dev/hydra/actions/workflows/ci.yml)
![Version](https://img.shields.io/badge/version-0.1.0-blue)
![Go](https://img.shields.io/badge/go-1.22%2B-00ADD8)
![License](https://img.shields.io/badge/license-Source--Available-red)
![Made by Elchi](https://img.shields.io/badge/made%20by-Elchi-8A2BE2)

</div>

---

## Overview

Hydra sits between your encoder and the streaming platforms. OBS pushes a single
RTMP stream to your server; Hydra decodes it once, produces a tailored rendition
for each destination, and pushes them out in parallel. When the source drops, a
fallback loop keeps every output alive so viewers never see "offline".

It is built for CPU-only hardware: one decode feeds every encode, copy targets
cost almost nothing, and the encoder profile per target is fully under your
control.

```
                       ┌──────────────── Hydra ────────────────┐
   OBS ──RTMP──▶ ingest ─▶ feeder ─▶ ffmpeg (single decode)     │──▶ Twitch
                       │                ├─ encode  1080p60       │──▶ YouTube
   source drops        │                ├─ encode  720x1280      │──▶ TikTok
   fallback loop ──────▶ feeder swap    └─ copy    passthrough   │──▶ archive
                       └────────────────────────────────────────┘
```

## Features

| | |
|---|---|
| Multi-platform fan-out | One ingest, many simultaneous RTMP/RTMPS outputs |
| Single-decode transcode | Decode once, encode per target, copy targets are free |
| Modular targets | Platform presets fill the defaults, every field is overridable |
| Seamless fallback | Looped "be right back" source keeps outputs live across drops |
| Live dashboard | Signal-flow view, encode stats, per-target state, ffmpeg console |
| Server CLI | `hydractl` over a local socket, no token needed on the box |
| Stream-key auth | Incoming feed is authenticated before anything starts |

## Quickstart

```sh
git clone https://github.com/Elchi-dev/hydra.git && cd hydra
go build -o hydra    ./cmd/hydra
go build -o hydractl ./cmd/hydractl

./hydra -wizard             # generate a config interactively (writes only on confirm)
# or copy and edit the example instead:
cp config.example.yaml hydra.yaml
$EDITOR hydra.yaml          # set stream_key, paste target keys

./hydra -config hydra.yaml
```

Diagnostics before going live:

```sh
./hydra -doctor            # detected CPU, encoders (verified), and recommendations
./hydra -benchmark         # measured sustained transcoding capacity for this host
```

In OBS set the server to `rtmp://<your-host>:1935/live/` and the stream key to
the value of `server.stream_key`. Open the dashboard at `http://127.0.0.1:8090`.

Requires Go 1.22+ and `ffmpeg` on PATH (tested with ffmpeg 6.1).

## Dashboard

Live signal path, encode FPS / speed / dropped frames, per-target status, the
ffmpeg console, and target toggles. Keep the HTTP listener on localhost or a VPN
interface; set `server.api_token` to require a bearer token (the dashboard then
accepts `?token=...`).

## CLI

`hydractl` talks to the local control socket, so no token is needed on the
server itself.

```sh
hydractl status            # phase, fps, speed, drops, uptime
hydractl targets           # list targets and profiles
hydractl enable  youtube   # toggle a target (applies on next stream)
hydractl disable tiktok
hydractl logs              # recent ffmpeg output
hydractl stop              # stop the current session
hydractl --url http://host:8090 --token <tok> status   # remote instead of socket
```

## Be right back

When the source disconnects, Hydra waits `brb.grace_seconds`, then swaps the
encoder input from the live feed to a looped video file without tearing down the
outbound connections. Platforms keep receiving a continuous stream and viewers
see a fallback screen. When the source returns within `brb.hold_seconds`, it
cuts straight back to live.

<details>
<summary>How the swap stays seamless</summary>

A feeder presents ffmpeg a single continuous FLV stream and rewrites timestamps
so they remain monotonic across the source switch. ffmpeg never observes an
end-of-stream, so platform connections stay open. The timestamp continuity is
covered by tests; validate the live source switch against your own setup, since
it depends on ffmpeg tolerating a mid-stream codec change.
</details>

## Configuration

```yaml
server:
  rtmp_listen: ":1935"
  http_listen: "127.0.0.1:8090"
  control_socket: "/run/hydra/hydra.sock"
  ingest_app: "live"
  stream_key: "change-me"
  api_token: ""

brb:
  enabled: true
  source: "/var/lib/hydra/brb.mp4"
  grace_seconds: 2
  hold_seconds: 120

targets:
  - name: twitch
    enabled: true
    platform: twitch        # preset fills url, bitrate, resolution
    key: "live_xxx"
    mode: transcode
    video: { bitrate: 6000k, resolution: 1920x1080, fps: 60, preset: veryfast }
    audio: { bitrate: 160k }
```

| Platform preset | Default ingest | Notes |
|---|---|---|
| `twitch` | live.twitch.tv | 6 Mbps soft cap, 2s keyframes |
| `youtube` | a.rtmp.youtube.com | up to 9 Mbps at 1080p60 |
| `kick` | global-contribute | per-account ingest region |
| `tiktok` | paste from TikTok LIVE | vertical 9:16 |
| `custom` | you set the URL | any RTMP/RTMPS endpoint |

See `config.example.yaml` for a full annotated configuration.

## Run as a service

```ini
# /etc/systemd/system/hydra.service
[Unit]
Description=Hydra RTMP relay
After=network-online.target

[Service]
ExecStart=/usr/local/bin/hydra -config /etc/hydra/hydra.yaml
Restart=on-failure
RuntimeDirectory=hydra
StateDirectory=hydra
User=hydra

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl enable --now hydra
hydractl status
```

## Roadmap

- Hardware-aware encoding: detect NVENC / QSV / VAAPI / x264 and pick per target
- Setup wizard that probes the machine and recommends settings (never auto-applies)
- Benchmark tool that reports realistic capacity for the current hardware
- Resource caps: bound CPU and memory usage
- Isolated per-target workers: independent reconnect and real per-target stats
- Native platform login so keys are fetched, not pasted
- Notifications, recording targets, metrics, and in-dashboard configuration

## License

Source-available under the Hydra Source-Available License. The source may be
viewed for reference; all other rights are reserved. The license converts to the
Apache License 2.0 one year after the first 1.0.0 release. See [LICENSE](LICENSE).

<div align="center">

**Made by Elchi**

</div>
