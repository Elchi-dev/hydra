# Changelog

All notable changes to Hydra are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Planned

- Hardware-aware encoding with detection of NVENC, QSV, VAAPI, and software encoders
- Setup wizard that probes the machine and recommends configuration without applying it
- Benchmark command that reports realistic transcoding capacity for the host
- Configurable CPU, thread, and memory limits
- Isolated per-target workers with independent reconnect and per-target statistics
- Native platform login so stream keys are retrieved instead of pasted
- Notifications, recording targets, and metrics

## [0.1.0] - 2026-06-09

First public release.

### Added

- RTMP ingest server with stream-key authentication
- Single-decode transcoding pipeline that fans one source out to many targets
- Copy-mode passthrough targets alongside per-target transcoded renditions
- Modular target configuration with platform presets for Twitch, YouTube, Kick,
  TikTok, and generic custom RTMP/RTMPS endpoints
- Seamless be-right-back fallback that keeps outputs alive when the source drops,
  with grace and hold timers and automatic cut-back on reconnect
- Continuous FLV feeder with monotonic timestamps across source switches
- Embedded web dashboard with live signal-flow view, encode statistics,
  per-target state, ffmpeg console, and target toggles over Server-Sent Events
- Optional bearer-token protection for the HTTP API and dashboard
- `hydractl` command-line tool over a local control socket, with status,
  targets, enable, disable, logs, stop, and version commands
- Configurable RTMP and HTTP listeners, ingest application, and ffmpeg path
- Structured logging with configurable level
- Integration test that runs the generated ffmpeg command end to end, and a unit
  test covering feeder timestamp continuity

[Unreleased]: https://github.com/Elchi-dev/hydra/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/Elchi-dev/hydra/releases/tag/v0.1.0
