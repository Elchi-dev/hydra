# Roadmap

Direction: give full control over every setting while keeping the defaults
simple enough to start streaming without configuration knowledge.

## Shipped

- RTMP ingest with stream-key auth
- Single-decode, multi-target transcoding with copy passthrough
- Modular platform presets (Twitch, YouTube, Kick, TikTok, custom)
- Seamless be-right-back fallback
- Web dashboard, `hydractl` CLI, API token
- Hardware detection with encoder verification and `doctor`

## In progress

- Benchmark: measure realistic transcoding capacity on the host
- Setup wizard: probe the machine, recommend a full config, write it only on
  explicit confirmation

## Next

- Automatic per-target encoder selection using verified hardware encoders
- Resource limits: bound CPU, threads, and memory
- Isolated per-target workers with independent reconnect and per-target stats

## Vertical and shorts layouts

Two paths, chosen per user preference:

- OBS plugin path (full control, some PC load): full integration with a vertical
  OBS plugin such as Aitum Vertical. The plugin sends a second vertical feed;
  Hydra accepts multiple ingest streams and routes each to its own target group,
  so the vertical composition is authored in OBS and Hydra handles fan-out and
  transcoding.
- Server-side path (minimal PC load): a layout engine that reframes a single
  feed into per-target compositions (crop, scale, background, repositioned
  camera) compiled to an ffmpeg filter graph, later editable in the dashboard.

Supporting work: multi-key ingest mapped to target groups; layout templates.

## Quality of life

- Config hot-reload
- Notifications (webhook, Discord) on disconnect, fallback, and worker events
- Recording targets
- Metrics endpoint and dashboard history

## Polish and distribution (final phase)

- Terminal polish: color output, clear status formatting across all commands
- Dashboard redesign: a more refined, modern interface
- End-to-end user-friendliness pass
- Packaging and easy installation: apt repository, AUR package, install script,
  prebuilt binaries, and systemd integration
- Full documentation set
