// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Elchi-dev/hydra/internal/build"
	"github.com/Elchi-dev/hydra/internal/hardware"
)

type wizTarget struct {
	name       string
	platform   string
	url        string
	key        string
	resolution string
	fps        int
	bitrate    string
}

// runWizard walks through an interactive setup and writes a config only after
// the user confirms. It never changes anything without explicit consent.
func runWizard(cfgPath string) {
	in := bufio.NewReader(os.Stdin)

	fmt.Printf("%s %s  %s\n", build.Name, build.Version, build.Credit)
	fmt.Println("Setup wizard. It suggests values from your hardware; nothing is written until you confirm.")
	fmt.Println()

	hw := hardware.Detect("ffmpeg")
	if !hw.FFmpegOK {
		fmt.Println("Note: ffmpeg could not be run. You can still create a config, but fix ffmpeg before streaming (see: hydra -doctor).")
	} else {
		fmt.Printf("Detected %d cores", hw.LogicalCores)
		if hw.CPUModel != "" {
			fmt.Printf(" (%s)", hw.CPUModel)
		}
		fmt.Println(".")
	}
	preset := recommendPreset(hw.LogicalCores)
	fmt.Println()

	streamKey := ask(in, "Ingest stream key (OBS authenticates with this)", randomKey())
	res := ask(in, "Canvas resolution", "1920x1080")
	fps := askInt(in, "Frame rate", 60)
	preset = ask(in, "x264 preset (lower = less CPU)", preset)
	fmt.Println()

	var targets []wizTarget
	if askYesNo(in, "Stream to Twitch?", true) {
		targets = append(targets, wizTarget{"twitch", "twitch", "", ask(in, "  Twitch stream key", ""), res, fps, "6000k"})
	}
	if askYesNo(in, "Stream to YouTube?", false) {
		targets = append(targets, wizTarget{"youtube", "youtube", "", ask(in, "  YouTube stream key", ""), res, fps, "9000k"})
	}
	if askYesNo(in, "Stream to TikTok (vertical)?", false) {
		url := ask(in, "  TikTok ingest URL", "")
		targets = append(targets, wizTarget{"tiktok", "tiktok", url, ask(in, "  TikTok stream key", ""), "720x1280", 30, "4000k"})
	}
	if askYesNo(in, "Add a custom RTMP target?", false) {
		url := ask(in, "  Custom ingest URL", "")
		targets = append(targets, wizTarget{"custom", "custom", url, ask(in, "  Custom stream key", ""), res, fps, "6000k"})
	}
	fmt.Println()

	brb := askYesNo(in, "Enable be-right-back fallback?", true)
	brbFile := "/var/lib/hydra/brb.mp4"
	if brb {
		brbFile = ask(in, "  BRB video file path", brbFile)
	}

	yaml := renderConfig(streamKey, preset, brb, brbFile, targets)
	fmt.Println("\n------------------------------------------------------------")
	fmt.Print(yaml)
	fmt.Println("------------------------------------------------------------")

	if fileExists(cfgPath) {
		if !askYesNo(in, cfgPath+" already exists. Overwrite it?", false) {
			fmt.Println("Not written. Copy the config above wherever you need it.")
			return
		}
	} else if !askYesNo(in, "Write this to "+cfgPath+"?", false) {
		fmt.Println("Not written. Copy the config above wherever you need it.")
		return
	}

	if err := os.WriteFile(cfgPath, []byte(yaml), 0o640); err != nil {
		fmt.Fprintf(os.Stderr, "could not write %s: %v\n", cfgPath, err)
		os.Exit(1)
	}
	fmt.Printf("Wrote %s. Start the server with: hydra -config %s\n", cfgPath, cfgPath)
}

func renderConfig(streamKey, preset string, brb bool, brbFile string, targets []wizTarget) string {
	var b strings.Builder
	b.WriteString("server:\n")
	b.WriteString("  rtmp_listen: \":1935\"\n")
	b.WriteString("  http_listen: \"127.0.0.1:8090\"\n")
	b.WriteString("  control_socket: \"/run/hydra/hydra.sock\"\n")
	b.WriteString("  ingest_app: \"live\"\n")
	fmt.Fprintf(&b, "  stream_key: %q\n", streamKey)
	b.WriteString("  api_token: \"\"\n\n")

	b.WriteString("brb:\n")
	fmt.Fprintf(&b, "  enabled: %v\n", brb)
	fmt.Fprintf(&b, "  source: %q\n", brbFile)
	b.WriteString("  grace_seconds: 2\n")
	b.WriteString("  hold_seconds: 120\n\n")

	b.WriteString("logging:\n  level: \"info\"\n\n")

	b.WriteString("targets:\n")
	if len(targets) == 0 {
		b.WriteString("  []\n")
		return b.String()
	}
	for _, t := range targets {
		fmt.Fprintf(&b, "  - name: %s\n", t.name)
		b.WriteString("    enabled: true\n")
		fmt.Fprintf(&b, "    platform: %s\n", t.platform)
		if t.url != "" {
			fmt.Fprintf(&b, "    url: %q\n", t.url)
		}
		fmt.Fprintf(&b, "    key: %q\n", t.key)
		b.WriteString("    mode: transcode\n")
		fmt.Fprintf(&b, "    video: { bitrate: %s, resolution: %s, fps: %d, preset: %s }\n", t.bitrate, t.resolution, t.fps, preset)
	}
	return b.String()
}

func recommendPreset(cores int) string {
	switch {
	case cores <= 0:
		return "veryfast"
	case cores < 4:
		return "ultrafast"
	case cores < 16:
		return "veryfast"
	default:
		return "faster"
	}
}

func randomKey() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return "change-me-please"
	}
	return "hy_" + hex.EncodeToString(buf)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// --- prompt helpers ---

func ask(in *bufio.Reader, prompt, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return def
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func askInt(in *bufio.Reader, prompt string, def int) int {
	s := ask(in, prompt, strconv.Itoa(def))
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func askYesNo(in *bufio.Reader, prompt string, def bool) bool {
	d := "y/N"
	if def {
		d = "Y/n"
	}
	fmt.Printf("%s [%s]: ", prompt, d)
	line, err := in.ReadString('\n')
	if err != nil && line == "" {
		return def
	}
	line = strings.ToLower(strings.TrimSpace(line))
	switch line {
	case "":
		return def
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return def
	}
}
