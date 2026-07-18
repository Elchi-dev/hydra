// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

// Package hardware probes the host for transcoding-relevant capabilities:
// available ffmpeg encoders, hardware acceleration backends, CPU, and memory.
// Detection is best-effort and never panics; missing data is reported as empty.
package hardware

import (
	"bufio"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Backend classifies an encoder by the acceleration it uses.
type Backend string

const (
	BackendSoftware     Backend = "software"
	BackendNVENC        Backend = "nvenc"
	BackendQSV          Backend = "qsv"
	BackendVAAPI        Backend = "vaapi"
	BackendAMF          Backend = "amf"
	BackendVideoToolbox Backend = "videotoolbox"
)

// backendPriority ranks backends from most to least preferred for auto-select.
var backendPriority = []Backend{
	BackendNVENC, BackendQSV, BackendVAAPI, BackendAMF, BackendVideoToolbox, BackendSoftware,
}

// Encoder is one ffmpeg video encoder classified by codec and backend.
type Encoder struct {
	Name    string  `json:"name"`
	Codec   string  `json:"codec"`
	Backend Backend `json:"backend"`
	// Available reports whether a probe confirmed the encoder actually runs on
	// this machine. Software encoders are always available; hardware encoders
	// are only trustworthy after Verify has probed them.
	Available bool `json:"available"`
	// Verified is true once Verify has attempted a probe for this encoder.
	Verified bool `json:"verified"`
}

// Info is the detected hardware profile.
type Info struct {
	CPUModel      string    `json:"cpu_model"`
	LogicalCores  int       `json:"logical_cores"`
	MemTotalMB    int       `json:"mem_total_mb"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	FFmpegVersion string    `json:"ffmpeg_version"`
	FFmpegOK      bool      `json:"ffmpeg_ok"`
	Hwaccels      []string  `json:"hwaccels"`
	Encoders      []Encoder `json:"encoders"`
}

// Detect gathers the hardware profile using the given ffmpeg binary. It always
// returns a usable Info, even if some probes fail.
func Detect(ffmpegBin string) *Info {
	info := &Info{
		LogicalCores: runtime.NumCPU(),
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
	}
	info.CPUModel = cpuModel()
	info.MemTotalMB = memTotalMB()
	info.FFmpegVersion = ffmpegVersion(ffmpegBin)
	info.FFmpegOK = ffmpegRunnable(ffmpegBin)
	if !info.FFmpegOK {
		return info
	}
	info.Encoders = detectEncoders(ffmpegBin)
	info.Hwaccels = detectHwaccels(ffmpegBin)
	info.verifyHardware(ffmpegBin)
	return info
}

// verifyHardware probes each hardware encoder with a minimal encode so the
// report reflects what actually runs on this machine, not just what ffmpeg was
// compiled with. Software encoders are assumed available. Probes run in
// parallel with a per-probe timeout and never affect process stability.
func (i *Info) verifyHardware(ffmpegBin string) {
	var wg sync.WaitGroup
	for idx := range i.Encoders {
		e := &i.Encoders[idx]
		if e.Backend == BackendSoftware {
			continue
		}
		wg.Add(1)
		go func(enc *Encoder) {
			defer wg.Done()
			enc.Available = probeEncoder(ffmpegBin, *enc)
			enc.Verified = true
		}(e)
	}
	wg.Wait()
}

// probeEncoder attempts a single-frame encode and reports whether it succeeded.
// Unlike the capability queries, a probe must exit cleanly (code 0) to count as
// available; error output alone is treated as failure.
func probeEncoder(bin string, e Encoder) bool {
	if bin == "" {
		bin = "ffmpeg"
	}
	cmd := exec.Command(bin, append([]string{"-hide_banner"}, probeArgs(e)...)...)
	if err := cmd.Start(); err != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		return false
	}
}

// probeArgs builds a minimal probe pipeline appropriate to the encoder backend.
func probeArgs(e Encoder) []string {
	src := []string{"-f", "lavfi", "-i", "color=c=black:s=128x128:r=5:d=1"}
	tail := []string{"-frames:v", "1", "-c:v", e.Name, "-f", "null", "-"}
	switch e.Backend {
	case BackendVAAPI:
		args := []string{"-loglevel", "error",
			"-init_hw_device", "vaapi=va:/dev/dri/renderD128", "-filter_hw_device", "va"}
		args = append(args, src...)
		args = append(args, "-vf", "format=nv12,hwupload")
		return append(args, tail...)
	case BackendQSV:
		args := []string{"-loglevel", "error", "-init_hw_device", "qsv=hw"}
		args = append(args, src...)
		args = append(args, "-vf", "format=nv12,hwupload=extra_hw_frames=16")
		return append(args, tail...)
	default: // nvenc, amf, videotoolbox
		args := []string{"-loglevel", "error"}
		args = append(args, src...)
		return append(args, tail...)
	}
}

// HasHardwareEncoder reports whether any verified hardware video encoder works.
func (i *Info) HasHardwareEncoder() bool {
	for _, e := range i.Encoders {
		if e.Backend != BackendSoftware && e.Available {
			return true
		}
	}
	return false
}

// EncodersFor returns the detected encoders for a codec (h264, hevc, av1, ...).
func (i *Info) EncodersFor(codec string) []Encoder {
	var out []Encoder
	for _, e := range i.Encoders {
		if e.Codec == codec {
			out = append(out, e)
		}
	}
	return out
}

// BestEncoderFor returns the preferred usable encoder for a codec, favoring
// verified hardware backends and falling back to software. Encoders that failed
// verification are never selected. The bool is false if none are usable.
func (i *Info) BestEncoderFor(codec string) (Encoder, bool) {
	candidates := i.EncodersFor(codec)
	if len(candidates) == 0 {
		return Encoder{}, false
	}
	for _, b := range backendPriority {
		for _, e := range candidates {
			if e.Backend == b && e.Available {
				return e, true
			}
		}
	}
	return Encoder{}, false
}

func runFFmpeg(bin string, args ...string) (string, bool) {
	if bin == "" {
		bin = "ffmpeg"
	}
	ctxArgs := append([]string{"-hide_banner"}, args...)
	cmd := exec.Command(bin, ctxArgs...)
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		return "", false
	}
	if err != nil && len(out) == 0 {
		return "", false
	}
	return string(out), true
}

// ffmpegRunnable reports whether the ffmpeg binary starts and exits cleanly.
// This distinguishes a working install from one that is present but broken
// (for example a missing shared library), which would otherwise look like a
// silent absence of encoders.
func ffmpegRunnable(bin string) bool {
	if bin == "" {
		bin = "ffmpeg"
	}
	cmd := exec.Command(bin, "-hide_banner", "-version")
	if err := cmd.Start(); err != nil {
		return false
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(8 * time.Second):
		_ = cmd.Process.Kill()
		return false
	}
}

func ffmpegVersion(bin string) string {
	out, ok := runFFmpeg(bin, "-version")
	if !ok {
		return ""
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	if sc.Scan() {
		fields := strings.Fields(sc.Text())
		for idx, f := range fields {
			if f == "version" && idx+1 < len(fields) {
				return fields[idx+1]
			}
		}
	}
	return ""
}

// detectEncoders parses `ffmpeg -encoders` and classifies video encoders.
func detectEncoders(bin string) []Encoder {
	out, ok := runFFmpeg(bin, "-encoders")
	if !ok {
		return nil
	}
	return parseEncoders(out)
}

func parseEncoders(out string) []Encoder {
	var encoders []Encoder
	sc := bufio.NewScanner(strings.NewReader(out))
	body := false
	for sc.Scan() {
		line := sc.Text()
		if !body {
			if strings.HasPrefix(strings.TrimSpace(line), "------") {
				body = true
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		flags := fields[0]
		if !strings.HasPrefix(flags, "V") {
			continue // video encoders only
		}
		name := fields[1]
		codec, backend, ok := classifyEncoder(name)
		if !ok {
			continue
		}
		encoders = append(encoders, Encoder{
			Name:      name,
			Codec:     codec,
			Backend:   backend,
			Available: backend == BackendSoftware,
			Verified:  backend == BackendSoftware,
		})
	}
	return encoders
}

// classifyEncoder maps an ffmpeg encoder name to a codec and backend. Only the
// encoders Hydra can meaningfully target are recognized.
func classifyEncoder(name string) (codec string, backend Backend, ok bool) {
	switch {
	case name == "libx264":
		return "h264", BackendSoftware, true
	case name == "libx265":
		return "hevc", BackendSoftware, true
	case name == "libsvtav1", name == "librav1e", name == "libaom-av1":
		return "av1", BackendSoftware, true
	case name == "libvpx-vp9":
		return "vp9", BackendSoftware, true
	}

	codec = ""
	switch {
	case strings.HasPrefix(name, "h264"):
		codec = "h264"
	case strings.HasPrefix(name, "hevc"), strings.HasPrefix(name, "h265"):
		codec = "hevc"
	case strings.HasPrefix(name, "av1"):
		codec = "av1"
	default:
		return "", "", false
	}

	switch {
	case strings.Contains(name, "nvenc"):
		backend = BackendNVENC
	case strings.Contains(name, "qsv"):
		backend = BackendQSV
	case strings.Contains(name, "vaapi"):
		backend = BackendVAAPI
	case strings.Contains(name, "amf"):
		backend = BackendAMF
	case strings.Contains(name, "videotoolbox"):
		backend = BackendVideoToolbox
	default:
		return "", "", false
	}
	return codec, backend, true
}

func detectHwaccels(bin string) []string {
	out, ok := runFFmpeg(bin, "-hwaccels")
	if !ok {
		return nil
	}
	var accels []string
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "Hardware acceleration methods") {
			continue
		}
		accels = append(accels, line)
	}
	return accels
}

func cpuModel() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "model name") {
			if _, v, ok := strings.Cut(line, ":"); ok {
				return strings.TrimSpace(v)
			}
		}
	}
	return ""
}

func memTotalMB() int {
	if runtime.GOOS != "linux" {
		return 0
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				kb, err := strconv.Atoi(fields[1])
				if err == nil {
					return kb / 1024
				}
			}
		}
	}
	return 0
}
