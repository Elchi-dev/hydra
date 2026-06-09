// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

// Package pipeline orchestrates the live path from RTMP ingest through the
// feeder and ffmpeg distributor to the configured platforms.
//
// The feeder presents ffmpeg a single continuous FLV stream and swaps the
// underlying source (live or BRB loop) while keeping timestamps monotonic.
// ffmpeg never observes an end-of-stream, so outbound platform connections
// remain open across a source switch.
package pipeline

import (
	"io"
	"sync"

	"github.com/yutopp/go-flv"
	flvtag "github.com/yutopp/go-flv/tag"
)

// SourceID identifies who is currently allowed to write into the feeder.
type SourceID string

const (
	SourceLive SourceID = "live"
	SourceBRB  SourceID = "brb"
)

// Feeder serializes FLV tags from the active source into one continuous stream.
type Feeder struct {
	mu     sync.Mutex
	enc    *flv.Encoder
	w      io.WriteCloser
	closed bool

	active SourceID
	// timestamp bookkeeping for continuity across source switches
	srcBase int64 // first source timestamp after activation; -1 if unset
	outBase int64 // output timestamp the current source started at
	lastOut int64 // last emitted timestamp (kept strictly increasing)
}

// NewFeeder wraps w (the ffmpeg distributor's stdin) in an FLV encoder.
func NewFeeder(w io.WriteCloser) (*Feeder, error) {
	enc, err := flv.NewEncoder(w, flv.FlagsAudio|flv.FlagsVideo)
	if err != nil {
		return nil, err
	}
	return &Feeder{
		enc:     enc,
		w:       w,
		active:  SourceLive,
		srcBase: -1,
		lastOut: 0,
	}, nil
}

// Activate makes src the source whose tags are forwarded. Tags from any other
// source are discarded. Switching rebases timestamps so the output clock stays
// monotonic and gap-free.
func (f *Feeder) Activate(src SourceID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.active == src {
		return
	}
	f.active = src
	f.srcBase = -1            // re-anchor on the next tag from this source
	f.outBase = f.lastOut + 1 // continue just past the last emitted timestamp
}

// Active reports the current source.
func (f *Feeder) Active() SourceID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.active
}

// Write forwards a tag if it comes from the active source, rewriting its
// timestamp for continuity. Tags from inactive sources are drained and dropped.
// The caller passes ownership of t; Write closes its payload reader.
func (f *Feeder) Write(src SourceID, t *flvtag.FlvTag) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.closed || src != f.active {
		t.Close() // drain the payload reader so nothing leaks
		return nil
	}

	ts := int64(t.Timestamp)
	if f.srcBase < 0 {
		f.srcBase = ts
	}
	out := f.outBase + (ts - f.srcBase)
	if out <= f.lastOut {
		out = f.lastOut + 1
	}
	f.lastOut = out
	t.Timestamp = uint32(out)

	return f.enc.Encode(t)
}

// Close closes the underlying writer (signals EOF to ffmpeg -> clean shutdown).
func (f *Feeder) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	return f.w.Close()
}
