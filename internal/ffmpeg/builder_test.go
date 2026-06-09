// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

package ffmpeg

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Elchi-dev/hydra/internal/config"
)

// TestBuildArgsStructure checks the decode-once / encode-many shape: one split
// into N branches, a map per transcode target, and copy targets passed through.
func TestBuildArgsStructure(t *testing.T) {
	targets := []*config.Target{
		{Name: "a", Mode: config.ModeTranscode, URL: "rtmp://x/app", Key: "k1",
			Video: config.VideoConfig{Resolution: "1920x1080", FPS: 60, Bitrate: "6000k", Preset: "veryfast", GOP: "2s", Codec: "libx264"},
			Audio: config.AudioConfig{Codec: "aac", Bitrate: "160k"}},
		{Name: "b", Mode: config.ModeTranscode, URL: "rtmp://y/app", Key: "k2",
			Video: config.VideoConfig{Resolution: "720x1280", FPS: 30, Bitrate: "4000k", Codec: "libx264"},
			Audio: config.AudioConfig{Codec: "aac", Bitrate: "128k"}},
		{Name: "c", Mode: config.ModeCopy, URL: "rtmp://z/app", Key: "k3"},
	}
	args, err := BuildArgs(targets, 3)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "split=2") {
		t.Errorf("expected split=2 for two transcode targets, got: %s", joined)
	}
	if !strings.Contains(joined, "scale=1920:1080") || !strings.Contains(joined, "scale=720:1280") {
		t.Errorf("missing per-target scale filters: %s", joined)
	}
	if strings.Count(joined, "-f flv") != 3 {
		t.Errorf("expected 3 flv outputs, got %d", strings.Count(joined, "-f flv"))
	}
	if !strings.Contains(joined, "-c copy") {
		t.Errorf("copy target should use -c copy: %s", joined)
	}
	if !strings.Contains(joined, "rtmp://x/app/k1") {
		t.Errorf("target URL not assembled: %s", joined)
	}
	if !strings.Contains(joined, "-progress pipe:3") {
		t.Errorf("progress fd not wired: %s", joined)
	}
}

func TestBuildArgsNoTargets(t *testing.T) {
	if _, err := BuildArgs(nil, 3); err == nil {
		t.Error("expected error with no targets")
	}
}

// TestPipelineWithRealFFmpeg feeds a synthetic FLV (testsrc) through the exact
// args the builder produces, writing to local files instead of RTMP, and checks
// ffmpeg accepts the command and produces valid multi-rendition output. This
// validates the real ffmpeg invocation end-to-end on the host's ffmpeg build.
func TestPipelineWithRealFFmpeg(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()

	// 1) Generate a 3s test FLV (what the feeder would stream from OBS).
	input := dir + "/in.flv"
	gen := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100",
		"-t", "3", "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", "-f", "flv", input)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("generate input flv: %v\n%s", err, out)
	}

	// 2) Build args with the builder for file targets (different renditions + copy).
	targets := []*config.Target{
		{Name: "r360", Mode: config.ModeTranscode, URL: dir + "/r360.flv",
			Video: config.VideoConfig{Resolution: "640x360", FPS: 30, Bitrate: "800k", Preset: "ultrafast", GOP: "2s", Codec: "libx264"},
			Audio: config.AudioConfig{Codec: "aac", Bitrate: "96k"}},
		{Name: "r240", Mode: config.ModeTranscode, URL: dir + "/r240.flv",
			Video: config.VideoConfig{Resolution: "426x240", FPS: 15, Bitrate: "400k", Preset: "ultrafast", GOP: "2s", Codec: "libx264"},
			Audio: config.AudioConfig{Codec: "aac", Bitrate: "64k"}},
		{Name: "passthrough", Mode: config.ModeCopy, URL: dir + "/copy.flv"},
	}
	args, err := BuildArgs(targets, 3)
	if err != nil {
		t.Fatal(err)
	}

	// 3) Run the Process wrapper, feeding the generated FLV into stdin.
	var lastProg Progress
	p := New("ffmpeg", args, func(pr Progress) { lastProg = pr })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stdin, err := p.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inF, err := os.Open(input)
	if err != nil {
		t.Fatal(err)
	}
	// Stream the bytes in, then close stdin to signal EOF (clean finish).
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, rerr := inF.Read(buf)
			if n > 0 {
				if _, werr := stdin.Write(buf[:n]); werr != nil {
					break
				}
			}
			if rerr != nil {
				break
			}
		}
		inF.Close()
		stdin.Close()
	}()

	select {
	case <-p.Done():
	case <-ctx.Done():
		t.Fatalf("ffmpeg timed out\nlog:\n%s", strings.Join(p.StderrTail(), "\n"))
	}
	if err := p.ExitErr(); err != nil {
		t.Fatalf("ffmpeg failed: %v\nlog:\n%s", err, strings.Join(p.StderrTail(), "\n"))
	}

	// 4) Validate each output exists and probes as the requested rendition.
	checks := []struct {
		file string
		w, h string
	}{
		{dir + "/r360.flv", "640", "360"},
		{dir + "/r240.flv", "426", "240"},
		{dir + "/copy.flv", "1280", "720"}, // copy keeps source size
	}
	for _, c := range checks {
		fi, err := os.Stat(c.file)
		if err != nil || fi.Size() == 0 {
			t.Fatalf("output %s missing/empty: %v", c.file, err)
		}
		dims := probe(t, c.file)
		if !strings.Contains(dims, c.w+"x"+c.h) {
			t.Errorf("%s: expected %sx%s, probe said %q", c.file, c.w, c.h, dims)
		}
	}
	if lastProg.FrameNum == 0 {
		t.Error("expected progress parser to capture frames")
	}
	t.Logf("pipeline ok, last progress: frame=%d fps=%.0f speed=%.2fx",
		lastProg.FrameNum, lastProg.FPS, lastProg.Speed)
}

func probe(t *testing.T, file string) string {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return ""
	}
	out, _ := exec.Command("ffprobe", "-v", "error",
		"-select_streams", "v:0", "-show_entries", "stream=width,height",
		"-of", "csv=p=0:s=x", file).Output()
	return strings.TrimSpace(string(out))
}
