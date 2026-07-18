// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

// Command hydractl is the server-side CLI for Hydra. By default it talks to the
// local control socket (no token needed); pass --url to reach a remote instance.
//
// Usage:
//
//	hydractl status                 show live state
//	hydractl targets                list targets
//	hydractl enable  <name>         enable a target (applies next stream)
//	hydractl disable <name>         disable a target
//	hydractl logs                   dump recent ffmpeg output
//	hydractl stop                   stop the current session
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Elchi-dev/hydra/internal/build"
	"github.com/Elchi-dev/hydra/internal/hardware"
)

func main() {
	sock := flag.String("socket", "/run/hydra/hydra.sock", "control socket path")
	url := flag.String("url", "", "HTTP base URL instead of the socket (e.g. http://127.0.0.1:8090)")
	token := flag.String("token", "", "API token (only needed with --url if configured)")
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	c := newClient(*sock, *url, *token)
	cmd := args[0]

	var err error
	switch cmd {
	case "status":
		err = c.status()
	case "targets":
		err = c.targets()
	case "enable", "disable":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "usage: hydractl %s <target-name>\n", cmd)
			os.Exit(2)
		}
		err = c.toggle(args[1], cmd == "enable")
	case "logs":
		err = c.logs()
	case "doctor":
		err = c.doctor()
	case "stop":
		err = c.stop()
	case "version", "--version", "-v":
		fmt.Println(build.VersionLine())
		return
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `hydractl - Hydra relay control

  hydractl status            live phase, fps, speed, drops, uptime
  hydractl targets           list configured targets
  hydractl enable  <name>    enable a target (applies on next stream)
  hydractl disable <name>    disable a target
  hydractl logs              recent ffmpeg encoder output
  hydractl doctor            hardware and encoder report with recommendations
  hydractl stop              stop the current session

flags:
  --socket <path>  control socket (default /run/hydra/hydra.sock)
  --url <base>     use HTTP instead of the socket
  --token <tok>    API token (with --url, if configured)

`+build.Credit+`
`)
}

// client speaks to the API either over a unix socket or a TCP base URL.
type client struct {
	http  *http.Client
	base  string
	token string
}

func newClient(sock, url, token string) *client {
	if url != "" {
		return &client{http: &http.Client{Timeout: 10 * time.Second}, base: strings.TrimRight(url, "/"), token: token}
	}
	// Unix socket transport; the host in the URL is ignored but required.
	hc := &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	return &client{http: hc, base: "http://unix"}
}

func (c *client) get(path string, out any) error {
	req, _ := http.NewRequest(http.MethodGet, c.base+path, nil)
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach hydra (%w), is the server running?", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *client) post(path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(http.MethodPost, c.base+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach hydra (%w), is the server running?", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// --- commands ---

func (c *client) status() error {
	var s struct {
		Phase       string  `json:"phase"`
		OutputFPS   float64 `json:"output_fps"`
		OutputSpeed float64 `json:"output_speed"`
		DropFrames  int64   `json:"drop_frames"`
		UptimeSec   int64   `json:"uptime_sec"`
		BRBActive   bool    `json:"brb_active"`
		LastEvent   string  `json:"last_event"`
		Targets     []struct {
			Name   string `json:"name"`
			Active bool   `json:"active"`
		} `json:"targets"`
	}
	if err := c.get("/api/state", &s); err != nil {
		return err
	}
	dot := map[string]string{"live": "●", "brb": "◐", "idle": "○"}[s.Phase]
	if dot == "" {
		dot = "○"
	}
	fmt.Printf("%s  %s\n", dot, strings.ToUpper(s.Phase))
	fmt.Printf("   fps %.0f   speed %.2fx   dropped %d   uptime %s\n",
		s.OutputFPS, s.OutputSpeed, s.DropFrames, fmtDur(s.UptimeSec))
	active := 0
	for _, t := range s.Targets {
		if t.Active {
			active++
		}
	}
	fmt.Printf("   targets on air: %d/%d\n", active, len(s.Targets))
	if s.LastEvent != "" {
		fmt.Printf("   last: %s\n", s.LastEvent)
	}
	return nil
}

func (c *client) targets() error {
	var cfg struct {
		Targets []struct {
			Name       string `json:"name"`
			Platform   string `json:"platform"`
			Enabled    bool   `json:"enabled"`
			Mode       string `json:"mode"`
			Resolution string `json:"resolution"`
			Bitrate    string `json:"bitrate"`
			FPS        int    `json:"fps"`
		} `json:"targets"`
	}
	if err := c.get("/api/config", &cfg); err != nil {
		return err
	}
	fmt.Printf("%-16s %-9s %-9s %-7s %s\n", "NAME", "PLATFORM", "MODE", "STATE", "PROFILE")
	for _, t := range cfg.Targets {
		state := "off"
		if t.Enabled {
			state = "on"
		}
		prof := "passthrough"
		if t.Mode != "copy" {
			prof = fmt.Sprintf("%s %s %dfps", emptyDash(t.Resolution), t.Bitrate, t.FPS)
		}
		fmt.Printf("%-16s %-9s %-9s %-7s %s\n", t.Name, t.Platform, t.Mode, state, prof)
	}
	return nil
}

func (c *client) toggle(name string, enabled bool) error {
	var res struct {
		OK           bool `json:"ok"`
		NeedsRestart bool `json:"needs_restart"`
	}
	err := c.post("/api/targets/toggle", map[string]any{"name": name, "enabled": enabled}, &res)
	if err != nil {
		return err
	}
	verb := "disabled"
	if enabled {
		verb = "enabled"
	}
	fmt.Printf("%s %s\n", name, verb)
	if res.NeedsRestart {
		fmt.Println("note: a stream is live, stop & re-stream from OBS to apply this change")
	}
	return nil
}

func (c *client) doctor() error {
	var res struct {
		Hardware hardware.Info `json:"hardware"`
	}
	if err := c.get("/api/doctor", &res); err != nil {
		return err
	}
	fmt.Print(res.Hardware.Report())
	return nil
}

func (c *client) logs() error {
	var res struct {
		Lines []string `json:"lines"`
	}
	if err := c.get("/api/logs", &res); err != nil {
		return err
	}
	if len(res.Lines) == 0 {
		fmt.Println("(no encoder output, nothing is streaming)")
		return nil
	}
	for _, l := range res.Lines {
		fmt.Println(l)
	}
	return nil
}

func (c *client) stop() error {
	if err := c.post("/api/control/stop", nil, nil); err != nil {
		return err
	}
	fmt.Println("session stopped")
	return nil
}

func fmtDur(s int64) string {
	if s <= 0 {
		return "0s"
	}
	h, m, sec := s/3600, (s%3600)/60, s%60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm", h, m)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, sec)
	default:
		return fmt.Sprintf("%ds", sec)
	}
}

func emptyDash(s string) string {
	if s == "" {
		return "source"
	}
	return s
}
