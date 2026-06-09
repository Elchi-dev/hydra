// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

package pipeline

import (
	"fmt"

	"github.com/Elchi-dev/hydra/internal/config"
)

// Config returns the active configuration.
func (m *Manager) Config() *config.Config { return m.cfg }

// Logs returns the most recent ffmpeg distributor log lines (empty if idle).
func (m *Manager) Logs() []string {
	m.mu.Lock()
	s := m.sess
	m.mu.Unlock()
	if s == nil || s.proc == nil {
		return nil
	}
	return s.proc.StderrTail()
}

// Active reports whether a session is live or in BRB.
func (m *Manager) Active() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sess != nil
}

// SetTargetEnabled flips a target on/off. The change applies to the next session
// (rebuilding ffmpeg mid-stream would interrupt all outputs), so if a session is
// live the caller is told a restart is needed.
func (m *Manager) SetTargetEnabled(name string, enabled bool) (needsRestart bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var found *config.Target
	for _, t := range m.cfg.Targets {
		if t.Name == name {
			found = t
			break
		}
	}
	if found == nil {
		return false, fmt.Errorf("no target named %q", name)
	}
	found.Enabled = enabled
	return m.sess != nil, nil
}
