// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

// Package api exposes the HTTP control/monitoring surface and serves the web UI.
package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Elchi-dev/hydra/internal/config"
	"github.com/Elchi-dev/hydra/internal/pipeline"
	"github.com/Elchi-dev/hydra/internal/state"
	"github.com/Elchi-dev/hydra/internal/web"
)

// Server is the HTTP API + dashboard.
type Server struct {
	cfg   *config.Config
	mgr   *pipeline.Manager
	store *state.Store
	log   *slog.Logger
}

// New builds the API server.
func New(cfg *config.Config, mgr *pipeline.Manager, store *state.Store, log *slog.Logger) *Server {
	return &Server{cfg: cfg, mgr: mgr, store: store, log: log}
}

// Handler returns the configured HTTP mux.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Static dashboard.
	mux.Handle("/", web.Handler())

	// API (token-guarded).
	mux.HandleFunc("/api/state", s.guard(s.handleState))
	mux.HandleFunc("/api/events", s.guard(s.handleEvents))
	mux.HandleFunc("/api/config", s.guard(s.handleConfig))
	mux.HandleFunc("/api/logs", s.guard(s.handleLogs))
	mux.HandleFunc("/api/targets/toggle", s.guard(s.handleToggle))
	mux.HandleFunc("/api/control/stop", s.guard(s.handleStop))

	return mux
}

// guard enforces the API token if one is configured.
func (s *Server) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Server.APIToken != "" {
			tok := r.Header.Get("Authorization")
			tok = strings.TrimPrefix(tok, "Bearer ")
			if tok == "" {
				tok = r.URL.Query().Get("token")
			}
			if tok != s.cfg.Server.APIToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next(w, r)
	}
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.store.Get())
}

// handleEvents streams snapshots as Server-Sent Events.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, unsub := s.store.Subscribe()
	defer unsub()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case snap := <-ch:
			b, _ := json.Marshal(snap)
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// configView is a sanitized config for the UI (stream keys redacted).
type configView struct {
	IngestApp  string             `json:"ingest_app"`
	RTMPListen string             `json:"rtmp_listen"`
	BRBEnabled bool               `json:"brb_enabled"`
	Targets    []targetConfigView `json:"targets"`
}

type targetConfigView struct {
	Name       string `json:"name"`
	Platform   string `json:"platform"`
	Enabled    bool   `json:"enabled"`
	Mode       string `json:"mode"`
	URL        string `json:"url"`
	Resolution string `json:"resolution"`
	Bitrate    string `json:"bitrate"`
	FPS        int    `json:"fps"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	c := s.mgr.Config()
	v := configView{
		IngestApp:  c.Server.IngestApp,
		RTMPListen: c.Server.RTMPListen,
		BRBEnabled: c.BRB.Enabled,
	}
	for _, t := range c.Targets {
		v.Targets = append(v.Targets, targetConfigView{
			Name: t.Name, Platform: t.Platform, Enabled: t.Enabled,
			Mode: string(t.Mode), URL: t.URL,
			Resolution: t.Video.Resolution, Bitrate: t.Video.Bitrate, FPS: t.Video.FPS,
		})
	}
	writeJSON(w, v)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"lines": s.mgr.Logs()})
}

func (s *Server) handleToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	restart, err := s.mgr.SetTargetEnabled(req.Name, req.Enabled)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{
		"ok":            true,
		"needs_restart": restart,
	})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	s.mgr.StopSession("stopped from dashboard")
	writeJSON(w, map[string]any{"ok": true})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
