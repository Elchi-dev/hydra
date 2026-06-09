// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

// Package state holds the live runtime state of the relay and broadcasts
// snapshots to subscribers (the web UI consumes these over SSE).
package state

import (
	"sync"
	"time"
)

// Phase describes what the relay is currently doing.
type Phase string

const (
	PhaseIdle     Phase = "idle"     // no input connected
	PhaseLive     Phase = "live"     // OBS connected, distributing
	PhaseBRB      Phase = "brb"      // input dropped, showing fallback
	PhaseStarting Phase = "starting" // spinning up encoder
	PhaseStopping Phase = "stopping" // tearing down
)

// TargetStat is the live status of one output target.
type TargetStat struct {
	Name      string  `json:"name"`
	Platform  string  `json:"platform"`
	Enabled   bool    `json:"enabled"`
	Mode      string  `json:"mode"`
	URL       string  `json:"url"` // base only, key redacted
	Active    bool    `json:"active"`
	BitrateKb float64 `json:"bitrate_kb"`
	Note      string  `json:"note"`
}

// Snapshot is a consistent view of the whole system at a moment in time.
type Snapshot struct {
	Phase        Phase        `json:"phase"`
	StreamKeyOK  bool         `json:"stream_key_ok"`
	InputBitrate float64      `json:"input_bitrate_kb"`
	OutputFPS    float64      `json:"output_fps"`
	OutputSpeed  float64      `json:"output_speed"`
	DropFrames   int64        `json:"drop_frames"`
	UptimeSec    int64        `json:"uptime_sec"`
	BRBActive    bool         `json:"brb_active"`
	Targets      []TargetStat `json:"targets"`
	LastEvent    string       `json:"last_event"`
	UpdatedAt    int64        `json:"updated_at"` // unix ms
}

// Store is the concurrency-safe state holder + broadcaster.
type Store struct {
	mu          sync.RWMutex
	snap        Snapshot
	liveStart   time.Time
	subscribers map[int]chan Snapshot
	nextSub     int
}

// New creates an empty store in the idle phase.
func New() *Store {
	return &Store{
		snap:        Snapshot{Phase: PhaseIdle},
		subscribers: map[int]chan Snapshot{},
	}
}

// Update applies fn to the snapshot under lock and broadcasts the result.
func (s *Store) Update(fn func(*Snapshot)) {
	s.mu.Lock()
	fn(&s.snap)
	if s.snap.Phase == PhaseLive && s.liveStart.IsZero() {
		s.liveStart = time.Now()
	}
	if s.snap.Phase == PhaseIdle {
		s.liveStart = time.Time{}
	}
	if !s.liveStart.IsZero() {
		s.snap.UptimeSec = int64(time.Since(s.liveStart).Seconds())
	}
	s.snap.UpdatedAt = time.Now().UnixMilli()
	cur := s.snap
	subs := make([]chan Snapshot, 0, len(s.subscribers))
	for _, ch := range s.subscribers {
		subs = append(subs, ch)
	}
	s.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- cur:
		default: // drop if subscriber is slow; they'll get the next one
		}
	}
}

// Get returns the current snapshot.
func (s *Store) Get() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// Subscribe registers a channel for snapshot updates and returns an unsubscribe
// func. The channel is buffered; slow consumers miss intermediate frames.
func (s *Store) Subscribe() (<-chan Snapshot, func()) {
	s.mu.Lock()
	id := s.nextSub
	s.nextSub++
	ch := make(chan Snapshot, 4)
	s.subscribers[id] = ch
	cur := s.snap
	s.mu.Unlock()

	// Prime with the current snapshot.
	ch <- cur

	return ch, func() {
		s.mu.Lock()
		if c, ok := s.subscribers[id]; ok {
			delete(s.subscribers, id)
			close(c)
		}
		s.mu.Unlock()
	}
}
