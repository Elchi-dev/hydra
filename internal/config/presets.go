// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

package config

// Platform presets supply sensible defaults per streaming service. A preset
// only fills fields left unset, so the config stays short while every
// value remains overridable. Adding a platform is just another entry here, or
// use platform "custom" and specify the URL yourself. Presets are applied
// automatically during Load (before validation), so an enabled target can rely
// on its preset URL without spelling it out.

// Preset captures the well-known defaults for a streaming platform.
type Preset struct {
	DefaultURL     string // ingest base URL used when the target leaves url empty
	MaxBitrateHint string // informational, surfaced in the UI
	Notes          string // short human hint about the platform's quirks
	apply          func(t *Target)
}

// Presets is the registry of known platforms, keyed by the `platform:` field.
var Presets = map[string]Preset{
	"twitch": {
		DefaultURL:     "rtmp://live.twitch.tv/app",
		MaxBitrateHint: "6000k (partner: higher)",
		Notes:          "Single ingest. 6 Mbps soft cap, keyframe interval 2s.",
		apply: func(t *Target) {
			fillVideo(t, "6000k", "1920x1080", 60, "veryfast")
			fillAudio(t, "160k")
		},
	},
	"youtube": {
		DefaultURL:     "rtmp://a.rtmp.youtube.com/live2",
		MaxBitrateHint: "9000k @ 1080p60",
		Notes:          "Generous bitrate. Backup ingest at b.rtmp.youtube.com.",
		apply: func(t *Target) {
			fillVideo(t, "9000k", "1920x1080", 60, "veryfast")
			fillAudio(t, "192k")
		},
	},
	"kick": {
		DefaultURL:     "rtmps://fa723fc1b171.global-contribute.live-video.net/app",
		MaxBitrateHint: "8000k",
		Notes:          "Per-account ingest region; check your Kick dashboard for the URL.",
		apply: func(t *Target) {
			fillVideo(t, "8000k", "1920x1080", 60, "veryfast")
			fillAudio(t, "160k")
		},
	},
	"tiktok": {
		DefaultURL:     "",
		MaxBitrateHint: "4000k vertical",
		Notes:          "Vertical 9:16. URL+key come from TikTok LIVE; paste them in.",
		apply: func(t *Target) {
			fillVideo(t, "4000k", "720x1280", 30, "veryfast")
			fillAudio(t, "128k")
		},
	},
	"custom": {
		DefaultURL:     "",
		MaxBitrateHint: "as configured",
		Notes:          "Generic RTMP/RTMPS endpoint. You control every field.",
		apply: func(t *Target) {
			fillVideo(t, "6000k", "", 0, "veryfast")
			fillAudio(t, "160k")
		},
	},
}

// ApplyPreset fills unset fields of a target from its platform preset. Unknown
// platforms fall back to "custom". Returns the preset for UI hints.
func ApplyPreset(t *Target) Preset {
	p, ok := Presets[t.Platform]
	if !ok {
		p = Presets["custom"]
	}
	if t.URL == "" && p.DefaultURL != "" {
		t.URL = p.DefaultURL
	}
	p.apply(t)
	return p
}

func fillVideo(t *Target, bitrate, res string, fps int, preset string) {
	v := &t.Video
	if v.Codec == "" {
		v.Codec = "libx264"
	}
	if v.Preset == "" {
		v.Preset = preset
	}
	if v.Bitrate == "" {
		v.Bitrate = bitrate
	}
	if v.Resolution == "" {
		v.Resolution = res
	}
	if v.FPS == 0 {
		v.FPS = fps
	}
	if v.GOP == "" {
		v.GOP = "2s"
	}
	if v.Profile == "" {
		v.Profile = "high"
	}
}

func fillAudio(t *Target, bitrate string) {
	a := &t.Audio
	if a.Codec == "" {
		a.Codec = "aac"
	}
	if a.Bitrate == "" {
		a.Bitrate = bitrate
	}
}
