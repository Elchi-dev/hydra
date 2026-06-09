// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

// Package ffmpeg builds ffmpeg invocations and manages the encoder process.
package ffmpeg

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Elchi-dev/hydra/internal/config"
)

// BuildArgs constructs the ffmpeg argument list for the distributor process.
//
// The incoming stream is decoded once and fanned out to every enabled target.
// Copy-mode targets are forwarded without re-encoding; transcode-mode targets
// each receive a scaled, re-encoded rendition derived from the shared decode
// via a filter_complex split. This minimizes CPU cost on encode-only hardware.
//
// progressFD is the file descriptor (>=3) ffmpeg writes -progress data to,
// wired up by the process via ExtraFiles.
func BuildArgs(targets []*config.Target, progressFD int) ([]string, error) {
	if len(targets) == 0 {
		return nil, fmt.Errorf("no enabled targets to stream to")
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-nostats",
		// Regenerate presentation timestamps if the input has gaps (matters for
		// the BRB source switch, where two encoders are spliced together).
		"-fflags", "+genpts",
		"-i", "pipe:0",
		"-progress", "pipe:" + strconv.Itoa(progressFD),
		"-stats_period", "1",
	}

	// Split transcode and copy targets.
	var transcode []*config.Target
	var copyT []*config.Target
	for _, t := range targets {
		if t.Mode == config.ModeCopy {
			copyT = append(copyT, t)
		} else {
			transcode = append(transcode, t)
		}
	}

	// Build the filter_complex for transcode targets: one decode, N branches.
	if len(transcode) > 0 {
		labels := make([]string, len(transcode))
		var splitOuts strings.Builder
		for i := range transcode {
			labels[i] = fmt.Sprintf("v%d", i)
			fmt.Fprintf(&splitOuts, "[s%d]", i)
		}
		var fc strings.Builder
		fmt.Fprintf(&fc, "[0:v]split=%d%s", len(transcode), splitOuts.String())
		for i, t := range transcode {
			fc.WriteString(";")
			fmt.Fprintf(&fc, "[s%d]%s[%s]", i, videoFilter(t), labels[i])
		}
		args = append(args, "-filter_complex", fc.String())

		// Per-target encoder outputs (each consumes one split branch).
		for i, t := range transcode {
			args = append(args, "-map", "["+labels[i]+"]")
			if hasAudio() {
				args = append(args, "-map", "0:a?")
			}
			args = append(args, videoEncoderArgs(t)...)
			args = append(args, audioEncoderArgs(t)...)
			args = append(args, "-f", "flv", t.FullURL())
		}
	}

	// Copy targets: forward original A/V with no re-encode.
	for _, t := range copyT {
		args = append(args,
			"-map", "0:v", "-map", "0:a?",
			"-c", "copy",
			"-f", "flv", t.FullURL(),
		)
	}

	return args, nil
}

// hasAudio is a hook; OBS always sends audio in practice. Kept as a function so
// future logic (e.g. probing) can plug in.
func hasAudio() bool { return true }

// videoFilter returns the per-branch filter chain (scale/fps/pixfmt).
func videoFilter(t *config.Target) string {
	var parts []string
	if t.Video.Resolution != "" {
		w, h := splitRes(t.Video.Resolution)
		if w > 0 && h > 0 {
			parts = append(parts, fmt.Sprintf("scale=%d:%d:flags=bicubic", w, h))
		}
	}
	if t.Video.FPS > 0 {
		parts = append(parts, fmt.Sprintf("fps=%d", t.Video.FPS))
	}
	// libx264 wants yuv420p for broad player compatibility.
	parts = append(parts, "format=yuv420p")
	return strings.Join(parts, ",")
}

// videoEncoderArgs builds the -c:v ... block for a transcode target.
func videoEncoderArgs(t *config.Target) []string {
	v := t.Video
	codec := v.Codec
	if codec == "" {
		codec = "libx264"
	}
	bitrate := v.Bitrate
	if bitrate == "" {
		bitrate = "6000k"
	}
	maxrate := v.Maxrate
	if maxrate == "" {
		maxrate = bitrate
	}
	bufsize := v.Bufsize
	if bufsize == "" {
		bufsize = doubleRate(bitrate)
	}
	preset := v.Preset
	if preset == "" {
		preset = "veryfast"
	}

	args := []string{
		"-c:v", codec,
		"-preset", preset,
		"-b:v", bitrate,
		"-maxrate", maxrate,
		"-bufsize", bufsize,
		"-pix_fmt", "yuv420p",
		"-sc_threshold", "0", // disable scene-cut so GOP stays fixed for platforms
	}
	if v.Profile != "" {
		args = append(args, "-profile:v", v.Profile)
	}
	// Fixed keyframe interval. Platforms strongly prefer a steady GOP (~2s).
	gopSec := gopSeconds(v.GOP)
	args = append(args, "-g", strconv.Itoa(gopFrames(gopSec, v.FPS)),
		"-keyint_min", strconv.Itoa(gopFrames(gopSec, v.FPS)),
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%g)", gopSec),
	)
	if v.Extra != "" {
		args = append(args, strings.Fields(v.Extra)...)
	}
	return args
}

func audioEncoderArgs(t *config.Target) []string {
	a := t.Audio
	codec := a.Codec
	if codec == "" {
		codec = "aac"
	}
	bitrate := a.Bitrate
	if bitrate == "" {
		bitrate = "160k"
	}
	args := []string{"-c:a", codec, "-b:a", bitrate}
	if a.Rate > 0 {
		args = append(args, "-ar", strconv.Itoa(a.Rate))
	}
	return args
}

// --- small parsing helpers ---

func splitRes(res string) (int, int) {
	parts := strings.SplitN(strings.ToLower(res), "x", 2)
	if len(parts) != 2 {
		return 0, 0
	}
	w, _ := strconv.Atoi(strings.TrimSpace(parts[0]))
	h, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
	return w, h
}

// gopSeconds parses "2s" / "2" into a float number of seconds (default 2).
func gopSeconds(s string) float64 {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimSuffix(s, "s")
	if s == "" {
		return 2
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 {
		return 2
	}
	return f
}

// gopFrames converts a GOP length in seconds to frames, given fps (fallback 60).
func gopFrames(sec float64, fps int) int {
	if fps <= 0 {
		fps = 60
	}
	n := int(sec * float64(fps))
	if n < 1 {
		n = 1
	}
	return n
}

// doubleRate doubles a bitrate string like "6000k" -> "12000k".
func doubleRate(br string) string {
	br = strings.TrimSpace(strings.ToLower(br))
	mult := 1
	suffix := ""
	switch {
	case strings.HasSuffix(br, "k"):
		mult, suffix = 1, "k"
		br = strings.TrimSuffix(br, "k")
	case strings.HasSuffix(br, "m"):
		mult, suffix = 1, "m"
		br = strings.TrimSuffix(br, "m")
	}
	n, err := strconv.Atoi(br)
	if err != nil {
		return "12000k"
	}
	return strconv.Itoa(n*2*mult) + suffix
}
