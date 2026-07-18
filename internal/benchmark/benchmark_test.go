// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

package benchmark

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func ffmpegRunnable() bool {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return false
	}
	return exec.Command("ffmpeg", "-hide_banner", "-version").Run() == nil
}

func TestRunLightProfile(t *testing.T) {
	if !ffmpegRunnable() {
		t.Skip("ffmpeg not runnable")
	}
	p := Profile{Encoder: "libx264", Resolution: "320x240", FPS: 15, Preset: "ultrafast", Bitrate: "300k"}
	o := Options{Duration: 2 * time.Second, MaxConcurrency: 2, Threshold: 1.0}

	res := Run(context.Background(), "ffmpeg", p, o)
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.Levels) == 0 {
		t.Fatal("expected at least one measured level")
	}
	if res.SingleSpeed <= 0 {
		t.Fatalf("expected positive single-stream speed, got %.2f", res.SingleSpeed)
	}
	if res.Sustained < 1 {
		t.Errorf("expected to sustain at least one light stream, got %d", res.Sustained)
	}
	t.Logf("single %.2fx, sustained %d, recommended %d", res.SingleSpeed, res.Sustained, res.Recommended)
}

func TestParseSpeed(t *testing.T) {
	cases := map[string]float64{"4.2x": 4.2, " 1.00x": 1.0, "0.5x": 0.5, "N/A": 0}
	for in, want := range cases {
		if got := parseSpeed(in); got != want {
			t.Errorf("parseSpeed(%q) = %v, want %v", in, got, want)
		}
	}
}
