// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

// Package benchmark measures how many concurrent transcodes the host can sustain
// at real-time speed for a given encode profile. It runs real ffmpeg encodes
// against a synthetic source and reads the reported speed factor.
package benchmark

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Profile describes the encode being measured.
type Profile struct {
	Encoder    string // ffmpeg encoder, e.g. libx264
	Resolution string // WxH, e.g. 1920x1080
	FPS        int
	Preset     string
	Bitrate    string
}

// Options tunes the benchmark run.
type Options struct {
	Duration       time.Duration // per-level encode duration
	MaxConcurrency int           // upper bound on parallel encodes
	Threshold      float64       // minimum speed factor considered real-time
}

// Level is the result of testing a single concurrency level.
type Level struct {
	Concurrency int     `json:"concurrency"`
	MinSpeed    float64 `json:"min_speed"`
	MeanSpeed   float64 `json:"mean_speed"`
	RealTime    bool    `json:"real_time"`
}

// Result is the outcome of a benchmark run.
type Result struct {
	Profile     Profile `json:"profile"`
	SingleSpeed float64 `json:"single_speed"`
	Sustained   int     `json:"sustained_streams"`
	Recommended int     `json:"recommended_streams"`
	Levels      []Level `json:"levels"`
	Note        string  `json:"note"`
}

func (o Options) withDefaults() Options {
	if o.Duration <= 0 {
		o.Duration = 8 * time.Second
	}
	if o.MaxConcurrency <= 0 {
		o.MaxConcurrency = 2*runtime.NumCPU() + 2
	}
	if o.Threshold <= 0 {
		o.Threshold = 1.0
	}
	return o
}

func (p Profile) withDefaults() Profile {
	if p.Encoder == "" {
		p.Encoder = "libx264"
	}
	if p.Resolution == "" {
		p.Resolution = "1920x1080"
	}
	if p.FPS <= 0 {
		p.FPS = 60
	}
	if p.Preset == "" {
		p.Preset = "veryfast"
	}
	if p.Bitrate == "" {
		p.Bitrate = "6000k"
	}
	return p
}

// Run measures sustained capacity using an estimate-then-verify search so only a
// handful of ffmpeg batches run. It never panics; encode failures count as zero.
func Run(ctx context.Context, ffmpegBin string, p Profile, o Options) *Result {
	p = p.withDefaults()
	o = o.withDefaults()
	res := &Result{Profile: p}

	tested := map[int]bool{}
	okAt := map[int]bool{}
	measure := func(n int) bool {
		if n < 1 {
			n = 1
		}
		if n > o.MaxConcurrency {
			n = o.MaxConcurrency
		}
		if tested[n] {
			return okAt[n]
		}
		minS, meanS := runLevel(ctx, ffmpegBin, p, n, o.Duration)
		tested[n] = true
		okAt[n] = minS >= o.Threshold
		res.Levels = append(res.Levels, Level{n, minS, meanS, okAt[n]})
		if n == 1 {
			res.SingleSpeed = minS
		}
		return okAt[n]
	}

	if !measure(1) {
		res.Sustained = 0
		res.Recommended = 0
		res.Note = "This machine cannot sustain even one stream at this profile in real time. Lower the resolution, fps, or preset."
		sortLevels(res)
		return res
	}

	est := int(res.SingleSpeed / o.Threshold)
	if est < 1 {
		est = 1
	}
	if est > o.MaxConcurrency {
		est = o.MaxConcurrency
	}

	best := 1
	if est > 1 && measure(est) {
		best = est
		for n := est + 1; n <= o.MaxConcurrency && n <= est+3; n++ {
			if measure(n) {
				best = n
			} else {
				break
			}
		}
	} else if est > 1 {
		for n := est - 1; n >= 1; n-- {
			if measure(n) {
				best = n
				break
			}
		}
	}

	res.Sustained = best
	res.Recommended = best - 1 // leave headroom for spikes and audio
	if res.Recommended < 1 {
		res.Recommended = 1
	}
	sortLevels(res)
	return res
}

func sortLevels(r *Result) {
	sort.Slice(r.Levels, func(i, j int) bool { return r.Levels[i].Concurrency < r.Levels[j].Concurrency })
}

// runLevel runs n concurrent encodes and returns the min and mean speed factor.
func runLevel(ctx context.Context, bin string, p Profile, n int, dur time.Duration) (minSpeed, meanSpeed float64) {
	speeds := make([]float64, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			speeds[idx] = runOne(ctx, bin, p, dur)
		}(i)
	}
	wg.Wait()

	minSpeed = -1
	var sum float64
	for _, s := range speeds {
		sum += s
		if minSpeed < 0 || s < minSpeed {
			minSpeed = s
		}
	}
	if minSpeed < 0 {
		minSpeed = 0
	}
	return minSpeed, sum / float64(n)
}

// runOne encodes a synthetic source and returns the final speed factor (0 on failure).
func runOne(ctx context.Context, bin string, p Profile, dur time.Duration) float64 {
	if bin == "" {
		bin = "ffmpeg"
	}
	seconds := int(dur.Seconds())
	if seconds < 1 {
		seconds = 1
	}
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", fmt.Sprintf("testsrc2=size=%s:rate=%d", p.Resolution, p.FPS),
		"-t", strconv.Itoa(seconds),
		"-c:v", p.Encoder, "-preset", p.Preset, "-b:v", p.Bitrate,
		"-progress", "pipe:1", "-f", "null", "-",
	}

	runCtx, cancel := context.WithTimeout(ctx, dur+20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return 0
	}
	if err := cmd.Start(); err != nil {
		return 0
	}

	var lastSpeed float64
	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok && k == "speed" {
			if s := parseSpeed(v); s > 0 {
				lastSpeed = s
			}
		}
	}
	_ = cmd.Wait()
	return lastSpeed
}

func parseSpeed(v string) float64 {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "x"))
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}
	return f
}

// Report renders a human-readable benchmark report.
func (r *Result) Report() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Profile\n  %s %dfps, preset %s, %s, encoder %s\n\n",
		r.Profile.Resolution, r.Profile.FPS, r.Profile.Preset, r.Profile.Bitrate, r.Profile.Encoder)

	fmt.Fprintf(&b, "Measured\n")
	fmt.Fprintf(&b, "  single stream speed   %.2fx real-time\n", r.SingleSpeed)
	for _, l := range r.Levels {
		mark := "ok"
		if !l.RealTime {
			mark = "drops below real-time"
		}
		fmt.Fprintf(&b, "  %2d parallel           min %.2fx  (%s)\n", l.Concurrency, l.MinSpeed, mark)
	}

	fmt.Fprintf(&b, "\nCapacity\n")
	if r.Sustained == 0 {
		fmt.Fprintf(&b, "  none at this profile\n")
	} else {
		fmt.Fprintf(&b, "  sustained             %d stream(s) at real-time\n", r.Sustained)
		fmt.Fprintf(&b, "  recommended           %d stream(s) with headroom\n", r.Recommended)
	}
	if r.Note != "" {
		fmt.Fprintf(&b, "\n%s\n", r.Note)
	}
	return b.String()
}
