// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

package pipeline

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/yutopp/go-flv"
	flvtag "github.com/yutopp/go-flv/tag"
)

// BRBSource loops a video file through ffmpeg and pushes the resulting FLV tags
// into the feeder. It only emits while the feeder has BRB activated; otherwise
// its tags are dropped by the feeder. It runs continuously in the background so
// the switch to BRB is instant (no encoder cold-start latency).
type BRBSource struct {
	ffmpegBin string
	file      string
	feeder    *Feeder

	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
	stop    context.CancelFunc
}

// NewBRBSource creates a BRB source for the given video file.
func NewBRBSource(ffmpegBin, file string, feeder *Feeder) *BRBSource {
	return &BRBSource{ffmpegBin: ffmpegBin, file: file, feeder: feeder}
}

// brbArgs encodes the loop file to a canonical H.264/AAC FLV on stdout.
// -re plays at real time so timestamps advance at wall-clock speed; the
// distributor re-scales to each target, so the exact size here is not critical.
func (b *BRBSource) brbArgs() []string {
	return []string{
		"-hide_banner", "-loglevel", "error",
		"-re",
		"-stream_loop", "-1",
		"-i", b.file,
		"-c:v", "libx264", "-preset", "veryfast", "-tune", "zerolatency",
		"-b:v", "4000k", "-maxrate", "4000k", "-bufsize", "8000k",
		"-pix_fmt", "yuv420p", "-r", "30", "-g", "60", "-sc_threshold", "0",
		"-c:a", "aac", "-b:a", "128k", "-ar", "44100",
		"-f", "flv", "pipe:1",
	}
}

// Start launches the background encoder and tag pump. Safe to call once.
func (b *BRBSource) Start(parent context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return nil
	}
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, b.ffmpegBin, b.brbArgs()...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return fmt.Errorf("start brb ffmpeg: %w", err)
	}
	b.cmd = cmd
	b.stop = cancel
	b.running = true

	go b.pump(stdout)
	go func() {
		_ = cmd.Wait()
		b.mu.Lock()
		b.running = false
		b.mu.Unlock()
	}()
	return nil
}

// pump reads the FLV stream from ffmpeg and forwards tags to the feeder.
func (b *BRBSource) pump(r io.Reader) {
	dec, err := flv.NewDecoder(r)
	if err != nil {
		return
	}
	for {
		var t flvtag.FlvTag
		if err := dec.Decode(&t); err != nil {
			return // ffmpeg ended or pipe closed
		}
		// Feeder forwards only when BRB is the active source, and consumes the
		// payload synchronously, so reusing the decoder buffer next loop is safe.
		if err := b.feeder.Write(SourceBRB, &t); err != nil {
			return
		}
	}
}

// Stop terminates the background encoder.
func (b *BRBSource) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stop != nil {
		b.stop()
	}
	b.running = false
}

// Running reports whether the background encoder is alive.
func (b *BRBSource) Running() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.running
}
