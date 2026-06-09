// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

// Package config defines Hydra's configuration model and loading.
//
// Hydra is configured by a single YAML file. The most important section is
// `targets`: each target is one output destination (Twitch, YouTube, a custom
// RTMP endpoint, ...). Targets are intentionally generic, adding a new
// platform never requires a code change, only a config entry. Built-in
// platform presets (see package targets) just fill in sane defaults so the
// config stays short.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the root of hydra.yaml.
type Config struct {
	Server  ServerConfig  `yaml:"server"`
	BRB     BRBConfig     `yaml:"brb"`
	Targets []*Target     `yaml:"targets"`
	Logging LoggingConfig `yaml:"logging"`
}

// ServerConfig holds listener and runtime settings.
type ServerConfig struct {
	// RTMPListen is where OBS connects, e.g. ":1935".
	RTMPListen string `yaml:"rtmp_listen"`
	// HTTPListen is the web UI + API bind address. Keep this on localhost or a
	// VPN interface; the dashboard has no auth of its own beyond the API token.
	HTTPListen string `yaml:"http_listen"`
	// ControlSocket is the unix socket the CLI (hydractl) talks to.
	ControlSocket string `yaml:"control_socket"`
	// IngestApp is the RTMP app name, i.e. rtmp://host/<ingest_app>/<stream_key>.
	IngestApp string `yaml:"ingest_app"`
	// StreamKey authenticates the incoming OBS stream.
	StreamKey string `yaml:"stream_key"`
	// FFmpegPath is the ffmpeg binary (default "ffmpeg" from PATH).
	FFmpegPath string `yaml:"ffmpeg_path"`
	// APIToken guards the HTTP API. Empty disables the check (localhost only!).
	APIToken string `yaml:"api_token"`
}

// BRBConfig controls the "be right back" fallback that keeps outputs alive when
// the incoming OBS connection drops.
type BRBConfig struct {
	Enabled bool   `yaml:"enabled"`
	Source  string `yaml:"source"` // path to a looped video file (e.g. brb.mp4)
	// GraceSeconds is how long to wait after a disconnect before cutting to BRB.
	GraceSeconds int `yaml:"grace_seconds"`
	// HoldSeconds is how long to keep outputs alive on BRB before giving up.
	HoldSeconds int `yaml:"hold_seconds"`
}

// LoggingConfig controls log verbosity.
type LoggingConfig struct {
	Level string `yaml:"level"` // debug | info | warn | error
}

// Mode selects how a target is produced from the source.
type Mode string

const (
	// ModeCopy forwards the source 1:1 without re-encoding (lowest CPU, no
	// per-platform resolution/bitrate control).
	ModeCopy Mode = "copy"
	// ModeTranscode re-encodes video (and audio) to the target's profile.
	ModeTranscode Mode = "transcode"
)

// Target is one output destination.
type Target struct {
	Name     string      `yaml:"name"`
	Enabled  bool        `yaml:"enabled"`
	Platform string      `yaml:"platform"` // preset key: twitch|youtube|kick|tiktok|custom
	URL      string      `yaml:"url"`      // base ingest URL (without the key)
	Key      string      `yaml:"key"`      // stream key for the destination
	Mode     Mode        `yaml:"mode"`
	Video    VideoConfig `yaml:"video"`
	Audio    AudioConfig `yaml:"audio"`
}

// VideoConfig is the per-target video encoder profile (used in transcode mode).
type VideoConfig struct {
	Codec      string `yaml:"codec"`      // libx264 (default), libx265, ...
	Preset     string `yaml:"preset"`     // ultrafast..veryslow
	Bitrate    string `yaml:"bitrate"`    // e.g. 6000k
	Maxrate    string `yaml:"maxrate"`    // defaults to bitrate
	Bufsize    string `yaml:"bufsize"`    // defaults to 2x bitrate
	Resolution string `yaml:"resolution"` // WxH, e.g. 1920x1080; empty = keep source
	FPS        int    `yaml:"fps"`        // 0 = keep source
	GOP        string `yaml:"gop"`        // keyframe interval in seconds, e.g. "2s"
	Profile    string `yaml:"profile"`    // h264 profile: main|high|baseline
	Extra      string `yaml:"extra"`      // raw extra ffmpeg video args, space separated
}

// AudioConfig is the per-target audio encoder profile.
type AudioConfig struct {
	Codec   string `yaml:"codec"`   // aac (default)
	Bitrate string `yaml:"bitrate"` // e.g. 160k
	Rate    int    `yaml:"rate"`    // sample rate, 0 = keep source
}

// FullURL combines URL and Key into the destination ffmpeg writes to.
func (t *Target) FullURL() string {
	base := strings.TrimRight(t.URL, "/")
	if t.Key == "" {
		return base
	}
	return base + "/" + t.Key
}

// GraceDuration returns the BRB grace period as a Duration.
func (b BRBConfig) GraceDuration() time.Duration {
	if b.GraceSeconds <= 0 {
		return 2 * time.Second
	}
	return time.Duration(b.GraceSeconds) * time.Second
}

// HoldDuration returns how long outputs are held on BRB.
func (b BRBConfig) HoldDuration() time.Duration {
	if b.HoldSeconds <= 0 {
		return 120 * time.Second
	}
	return time.Duration(b.HoldSeconds) * time.Second
}

// Load reads and validates a config file, applying defaults and presets.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	c.applyDefaults()
	for _, t := range c.Targets {
		ApplyPreset(t)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.Server.RTMPListen == "" {
		c.Server.RTMPListen = ":1935"
	}
	if c.Server.HTTPListen == "" {
		c.Server.HTTPListen = "127.0.0.1:8090"
	}
	if c.Server.ControlSocket == "" {
		c.Server.ControlSocket = "/run/hydra/hydra.sock"
	}
	if c.Server.IngestApp == "" {
		c.Server.IngestApp = "live"
	}
	if c.Server.FFmpegPath == "" {
		c.Server.FFmpegPath = "ffmpeg"
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	for _, t := range c.Targets {
		if t.Mode == "" {
			t.Mode = ModeTranscode
		}
	}
}

// Validate checks the config for fatal problems.
func (c *Config) Validate() error {
	if c.Server.StreamKey == "" {
		return fmt.Errorf("server.stream_key must be set (this is what OBS authenticates with)")
	}
	seen := map[string]bool{}
	for i, t := range c.Targets {
		if t.Name == "" {
			return fmt.Errorf("targets[%d]: name is required", i)
		}
		if seen[t.Name] {
			return fmt.Errorf("duplicate target name %q", t.Name)
		}
		seen[t.Name] = true
		if t.Mode != ModeCopy && t.Mode != ModeTranscode {
			return fmt.Errorf("target %q: mode must be 'copy' or 'transcode'", t.Name)
		}
		if t.Enabled && t.URL == "" {
			return fmt.Errorf("target %q: url is required when enabled", t.Name)
		}
	}
	if c.BRB.Enabled && c.BRB.Source == "" {
		return fmt.Errorf("brb.enabled is true but brb.source (video file) is empty")
	}
	return nil
}

// EnabledTargets returns only the targets that are switched on.
func (c *Config) EnabledTargets() []*Target {
	var out []*Target
	for _, t := range c.Targets {
		if t.Enabled {
			out = append(out, t)
		}
	}
	return out
}
