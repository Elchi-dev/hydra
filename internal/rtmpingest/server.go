// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

// Package rtmpingest runs the RTMP server OBS connects to. It authenticates the
// publish by stream key and forwards audio/video/metadata tags to the pipeline
// manager.
package rtmpingest

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"net"

	"github.com/sirupsen/logrus"
	flvtag "github.com/yutopp/go-flv/tag"
	"github.com/yutopp/go-rtmp"
	rtmpmsg "github.com/yutopp/go-rtmp/message"

	"github.com/Elchi-dev/hydra/internal/config"
	"github.com/Elchi-dev/hydra/internal/pipeline"
)

// Server is the RTMP ingest listener.
type Server struct {
	cfg *config.Config
	mgr *pipeline.Manager
	log *slog.Logger
	srv *rtmp.Server
	ln  net.Listener
}

// New creates an ingest server.
func New(cfg *config.Config, mgr *pipeline.Manager, log *slog.Logger) *Server {
	return &Server{cfg: cfg, mgr: mgr, log: log}
}

// Listen binds the RTMP port. Call Serve afterwards.
func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", s.cfg.Server.RTMPListen)
	if err != nil {
		return fmt.Errorf("rtmp listen %s: %w", s.cfg.Server.RTMPListen, err)
	}
	s.ln = ln

	rtmpLog := logrus.New()
	rtmpLog.SetLevel(logrus.WarnLevel)

	s.srv = rtmp.NewServer(&rtmp.ServerConfig{
		OnConnect: func(conn net.Conn) (io.ReadWriteCloser, *rtmp.ConnConfig) {
			h := &handler{cfg: s.cfg, mgr: s.mgr, log: s.log}
			return conn, &rtmp.ConnConfig{
				Handler: h,
				Logger:  rtmpLog,
				ControlState: rtmp.StreamControlStateConfig{
					DefaultBandwidthWindowSize: 6 * 1024 * 1024 / 8,
				},
			}
		},
	})
	return nil
}

// Serve blocks serving RTMP connections.
func (s *Server) Serve() error {
	s.log.Info("rtmp ingest listening", "addr", s.cfg.Server.RTMPListen,
		"url", fmt.Sprintf("rtmp://<host>%s/%s/<stream_key>", portOf(s.cfg.Server.RTMPListen), s.cfg.Server.IngestApp))
	return s.srv.Serve(s.ln)
}

// Close stops the server.
func (s *Server) Close() error {
	if s.srv != nil {
		return s.srv.Close()
	}
	return nil
}

func portOf(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return ""
	}
	return ":" + port
}

// handler is one RTMP connection.
type handler struct {
	rtmp.DefaultHandler
	cfg *config.Config
	mgr *pipeline.Manager
	log *slog.Logger

	publishing bool
}

var _ rtmp.Handler = (*handler)(nil)

func (h *handler) OnConnect(_ uint32, cmd *rtmpmsg.NetConnectionConnect) error {
	// Log app-name mismatches without rejecting; some encoders set it loosely.
	if cmd != nil && cmd.Command.App != "" && cmd.Command.App != h.cfg.Server.IngestApp {
		h.log.Warn("rtmp app mismatch", "got", cmd.Command.App, "want", h.cfg.Server.IngestApp)
	}
	return nil
}

func (h *handler) OnPublish(_ *rtmp.StreamContext, _ uint32, cmd *rtmpmsg.NetStreamPublish) error {
	if cmd.PublishingName != h.cfg.Server.StreamKey {
		h.log.Warn("rejected publish: bad stream key")
		return fmt.Errorf("unauthorized stream key")
	}
	if err := h.mgr.OnPublish(); err != nil {
		h.log.Error("failed to start session", "err", err)
		return err
	}
	h.publishing = true
	return nil
}

func (h *handler) OnSetDataFrame(timestamp uint32, data *rtmpmsg.NetStreamSetDataFrame) error {
	if !h.publishing {
		return nil
	}
	var script flvtag.ScriptData
	if err := flvtag.DecodeScriptData(bytes.NewReader(data.Payload), &script); err != nil {
		return nil // ignore unparseable metadata
	}
	h.mgr.WriteLive(&flvtag.FlvTag{
		TagType:   flvtag.TagTypeScriptData,
		Timestamp: timestamp,
		Data:      &script,
	})
	return nil
}

func (h *handler) OnAudio(timestamp uint32, payload io.Reader) error {
	if !h.publishing {
		return nil
	}
	var audio flvtag.AudioData
	if err := flvtag.DecodeAudioData(payload, &audio); err != nil {
		return err
	}
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, audio.Data); err != nil {
		return err
	}
	audio.Data = buf
	h.mgr.WriteLive(&flvtag.FlvTag{
		TagType:   flvtag.TagTypeAudio,
		Timestamp: timestamp,
		Data:      &audio,
	})
	return nil
}

func (h *handler) OnVideo(timestamp uint32, payload io.Reader) error {
	if !h.publishing {
		return nil
	}
	var video flvtag.VideoData
	if err := flvtag.DecodeVideoData(payload, &video); err != nil {
		return err
	}
	// Deep copy: the payload buffer is recycled after this returns.
	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, video.Data); err != nil {
		return err
	}
	video.Data = buf
	h.mgr.WriteLive(&flvtag.FlvTag{
		TagType:   flvtag.TagTypeVideo,
		Timestamp: timestamp,
		Data:      &video,
	})
	return nil
}

func (h *handler) OnClose() {
	if h.publishing {
		h.publishing = false
		h.mgr.OnDisconnect()
	}
}
