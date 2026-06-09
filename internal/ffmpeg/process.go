// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

package ffmpeg

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Progress is one snapshot parsed from ffmpeg's -progress output.
type Progress struct {
	FrameNum   int64
	FPS        float64
	BitrateKb  float64 // current output bitrate in kbit/s
	TotalBytes int64
	DropFrames int64
	DupFrames  int64
	OutTimeMs  int64 // stream position in ms
	Speed      float64
	UpdatedAt  time.Time
}

// Process wraps a running ffmpeg distributor.
type Process struct {
	bin  string
	args []string

	cmd        *exec.Cmd
	stdin      io.WriteCloser
	progressR  *os.File
	progressW  *os.File
	onProgress func(Progress)

	mu        sync.Mutex
	stderrBuf *ringBuffer
	startedAt time.Time
	exitErr   error
	done      chan struct{}
}

// New creates an unstarted process. bin is the ffmpeg path.
func New(bin string, args []string, onProgress func(Progress)) *Process {
	return &Process{
		bin:        bin,
		args:       args,
		onProgress: onProgress,
		stderrBuf:  newRingBuffer(400), // keep last 400 log lines
		done:       make(chan struct{}),
	}
}

// Start launches ffmpeg. Write the FLV feed to the returned stdin writer.
func (p *Process) Start(ctx context.Context) (io.WriteCloser, error) {
	// fd 3 carries -progress key/value output.
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	p.progressR, p.progressW = pr, pw

	p.cmd = exec.CommandContext(ctx, p.bin, p.args...)
	p.cmd.ExtraFiles = []*os.File{pw} // becomes fd 3 in the child

	stdin, err := p.cmd.StdinPipe()
	if err != nil {
		pr.Close()
		pw.Close()
		return nil, err
	}
	p.stdin = stdin

	stderr, err := p.cmd.StderrPipe()
	if err != nil {
		pr.Close()
		pw.Close()
		return nil, err
	}

	if err := p.cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		return nil, err
	}
	p.startedAt = time.Now()

	// Child holds its own copy of the write end now.
	pw.Close()

	go p.readProgress()
	go p.readStderr(stderr)
	go func() {
		p.exitErr = p.cmd.Wait()
		p.progressR.Close()
		close(p.done)
	}()

	return stdin, nil
}

// Done is closed when ffmpeg exits.
func (p *Process) Done() <-chan struct{} { return p.done }

// ExitErr returns the wait error (nil if still running or clean exit).
func (p *Process) ExitErr() error { return p.exitErr }

// Stop closes stdin (graceful) and, after a grace period, kills the process.
func (p *Process) Stop(grace time.Duration) {
	if p.stdin != nil {
		_ = p.stdin.Close()
	}
	select {
	case <-p.done:
		return
	case <-time.After(grace):
		if p.cmd != nil && p.cmd.Process != nil {
			_ = p.cmd.Process.Kill()
		}
	}
}

// StderrTail returns the most recent ffmpeg log lines.
func (p *Process) StderrTail() []string { return p.stderrBuf.lines() }

func (p *Process) readProgress() {
	scanner := bufio.NewScanner(p.progressR)
	cur := Progress{}
	for scanner.Scan() {
		line := scanner.Text()
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "frame":
			cur.FrameNum, _ = strconv.ParseInt(v, 10, 64)
		case "fps":
			cur.FPS, _ = strconv.ParseFloat(v, 64)
		case "bitrate":
			cur.BitrateKb = parseBitrate(v)
		case "total_size":
			cur.TotalBytes, _ = strconv.ParseInt(v, 10, 64)
		case "drop_frames":
			cur.DropFrames, _ = strconv.ParseInt(v, 10, 64)
		case "dup_frames":
			cur.DupFrames, _ = strconv.ParseInt(v, 10, 64)
		case "out_time_ms":
			cur.OutTimeMs, _ = strconv.ParseInt(v, 10, 64)
		case "speed":
			cur.Speed = parseSpeed(v)
		case "progress":
			// End of a block ("continue" or "end"): emit the snapshot.
			cur.UpdatedAt = time.Now()
			if p.onProgress != nil {
				p.onProgress(cur)
			}
			next := Progress{} // reset for next block
			cur = next
		}
	}
}

func (p *Process) readStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		p.stderrBuf.add(scanner.Text())
	}
}

func parseBitrate(v string) float64 {
	// e.g. "6000.1kbits/s"
	v = strings.TrimSpace(strings.TrimSuffix(v, "bits/s"))
	mult := 1.0
	if strings.HasSuffix(v, "k") {
		v = strings.TrimSuffix(v, "k")
		mult = 1
	} else if strings.HasSuffix(v, "m") {
		v = strings.TrimSuffix(v, "m")
		mult = 1000
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f * mult
}

func parseSpeed(v string) float64 {
	v = strings.TrimSpace(strings.TrimSuffix(v, "x"))
	f, _ := strconv.ParseFloat(v, 64)
	return f
}

// ringBuffer keeps the last N strings.
type ringBuffer struct {
	mu   sync.Mutex
	buf  []string
	size int
}

func newRingBuffer(size int) *ringBuffer { return &ringBuffer{size: size} }

func (r *ringBuffer) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, s)
	if len(r.buf) > r.size {
		r.buf = r.buf[len(r.buf)-r.size:]
	}
}

func (r *ringBuffer) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.buf))
	copy(out, r.buf)
	return out
}
