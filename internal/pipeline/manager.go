// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

package pipeline

import (
	"context"
	"log/slog"
	"sync"
	"time"

	flvtag "github.com/yutopp/go-flv/tag"

	"github.com/Elchi-dev/hydra/internal/config"
	"github.com/Elchi-dev/hydra/internal/ffmpeg"
	"github.com/Elchi-dev/hydra/internal/state"
)

// Manager owns the live session and drives source switching.
type Manager struct {
	cfg   *config.Config
	store *state.Store
	log   *slog.Logger

	mu   sync.Mutex
	sess *session
}

type session struct {
	ctx    context.Context
	cancel context.CancelFunc
	proc   *ffmpeg.Process
	feeder *Feeder
	brb    *BRBSource

	liveUp        bool
	graceTimer    *time.Timer
	teardownTimer *time.Timer
}

// NewManager wires the relay manager.
func NewManager(cfg *config.Config, store *state.Store, log *slog.Logger) *Manager {
	return &Manager{cfg: cfg, store: store, log: log}
}

// OnPublish is called when an authenticated OBS stream starts. It (re)starts a
// session if needed and makes the live source active. Reconnecting during a BRB
// hold seamlessly cuts back to live.
func (m *Manager) OnPublish() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.sess == nil {
		if err := m.startSessionLocked(); err != nil {
			return err
		}
	}
	s := m.sess
	s.liveUp = true
	m.cancelTimersLocked(s)
	s.feeder.Activate(SourceLive)
	m.store.Update(func(sn *state.Snapshot) {
		sn.Phase = state.PhaseLive
		sn.StreamKeyOK = true
		sn.BRBActive = false
		sn.LastEvent = "OBS connected"
	})
	m.log.Info("live source connected")
	return nil
}

// WriteLive forwards a live FLV tag into the pipeline.
func (m *Manager) WriteLive(t *flvtag.FlvTag) {
	m.mu.Lock()
	s := m.sess
	m.mu.Unlock()
	if s == nil {
		t.Close()
		return
	}
	if err := s.feeder.Write(SourceLive, t); err != nil {
		m.log.Error("feeder write failed; tearing down", "err", err)
		m.StopSession("encoder error")
	}
}

// OnDisconnect is called when the OBS connection drops. After a grace period the
// session either cuts to BRB (keeping outputs alive) or shuts down.
func (m *Manager) OnDisconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sess
	if s == nil {
		return
	}
	s.liveUp = false
	m.store.Update(func(sn *state.Snapshot) { sn.LastEvent = "OBS disconnected" })
	m.log.Warn("live source dropped", "grace", m.cfg.BRB.GraceDuration())

	if s.graceTimer != nil {
		s.graceTimer.Stop()
	}
	s.graceTimer = time.AfterFunc(m.cfg.BRB.GraceDuration(), m.onGrace)
}

// onGrace fires after the post-disconnect grace window.
func (m *Manager) onGrace() {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sess
	if s == nil || s.liveUp {
		return // reconnected in time, nothing to do
	}

	if m.cfg.BRB.Enabled && s.brb != nil {
		s.feeder.Activate(SourceBRB)
		m.store.Update(func(sn *state.Snapshot) {
			sn.Phase = state.PhaseBRB
			sn.BRBActive = true
			sn.LastEvent = "Showing BRB screen"
		})
		m.log.Info("switched to BRB", "hold", m.cfg.BRB.HoldDuration())
		// Hold outputs alive on BRB, then give up if OBS never returns.
		if s.teardownTimer != nil {
			s.teardownTimer.Stop()
		}
		s.teardownTimer = time.AfterFunc(m.cfg.BRB.HoldDuration(), func() {
			m.StopSession("BRB hold expired")
		})
		return
	}

	// No BRB configured: end the session.
	go m.StopSession("input dropped, BRB disabled")
}

// StopSession tears down the live session and returns to idle.
func (m *Manager) StopSession(reason string) {
	m.mu.Lock()
	s := m.sess
	m.sess = nil
	m.mu.Unlock()
	if s == nil {
		return
	}
	m.cancelTimers(s)

	m.store.Update(func(sn *state.Snapshot) {
		sn.Phase = state.PhaseStopping
		sn.LastEvent = "Stopping: " + reason
	})

	if s.brb != nil {
		s.brb.Stop()
	}
	if s.feeder != nil {
		_ = s.feeder.Close() // EOF -> ffmpeg flushes and exits
	}
	if s.proc != nil {
		s.proc.Stop(3 * time.Second)
	}
	s.cancel()

	m.store.Update(func(sn *state.Snapshot) {
		sn.Phase = state.PhaseIdle
		sn.BRBActive = false
		sn.OutputFPS = 0
		sn.OutputSpeed = 0
		for i := range sn.Targets {
			sn.Targets[i].Active = false
			sn.Targets[i].BitrateKb = 0
		}
		sn.LastEvent = "Idle: " + reason
	})
	m.log.Info("session stopped", "reason", reason)
}

// startSessionLocked builds a new distributor + feeder (+ BRB). Caller holds mu.
func (m *Manager) startSessionLocked() error {
	targets := m.cfg.EnabledTargets()
	m.store.Update(func(sn *state.Snapshot) { sn.Phase = state.PhaseStarting })

	progressFD := 3 // first ExtraFiles entry
	args, err := ffmpeg.BuildArgs(targets, progressFD)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	proc := ffmpeg.New(m.cfg.Server.FFmpegPath, args, m.onProgress)
	stdin, err := proc.Start(ctx)
	if err != nil {
		cancel()
		return err
	}

	feeder, err := NewFeeder(stdin)
	if err != nil {
		cancel()
		return err
	}

	s := &session{ctx: ctx, cancel: cancel, proc: proc, feeder: feeder, liveUp: false}

	if m.cfg.BRB.Enabled {
		s.brb = NewBRBSource(m.cfg.Server.FFmpegPath, m.cfg.BRB.Source, feeder)
		if err := s.brb.Start(ctx); err != nil {
			m.log.Warn("BRB source failed to start; continuing without it", "err", err)
			s.brb = nil
		}
	}

	// If the distributor dies on its own, clean up.
	go func() {
		<-proc.Done()
		m.log.Warn("distributor exited", "err", proc.ExitErr())
		m.StopSession("distributor exited")
	}()

	m.sess = s

	// Seed target rows in the snapshot.
	rows := make([]state.TargetStat, 0, len(targets))
	for _, t := range targets {
		rows = append(rows, state.TargetStat{
			Name: t.Name, Platform: t.Platform, Enabled: true,
			Mode: string(t.Mode), URL: t.URL, Active: true,
		})
	}
	m.store.Update(func(sn *state.Snapshot) { sn.Targets = rows })
	m.log.Info("session started", "targets", len(targets))
	return nil
}

func (m *Manager) onProgress(p ffmpeg.Progress) {
	m.store.Update(func(sn *state.Snapshot) {
		sn.OutputFPS = p.FPS
		sn.OutputSpeed = p.Speed
		sn.DropFrames = p.DropFrames
	})
}

func (m *Manager) cancelTimers(s *session) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelTimersLocked(s)
}

func (m *Manager) cancelTimersLocked(s *session) {
	if s.graceTimer != nil {
		s.graceTimer.Stop()
		s.graceTimer = nil
	}
	if s.teardownTimer != nil {
		s.teardownTimer.Stop()
		s.teardownTimer = nil
	}
}
