// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

package hardware

import (
	"fmt"
	"sort"
	"strings"
)

// Recommendations returns qualitative guidance based on the detected profile.
// Precise capacity figures come from the benchmark command, not from here.
func (i *Info) Recommendations() []string {
	var recs []string

	if !i.FFmpegOK {
		recs = append(recs, "ffmpeg could not be run. It is missing or broken (for example a "+
			"missing shared library after a partial system upgrade). Reinstall or update "+
			"ffmpeg; no encoding is possible until it runs.")
		return recs
	}

	if i.HasHardwareEncoder() {
		var names []string
		for _, e := range i.Encoders {
			if e.Backend != BackendSoftware && e.Available {
				names = append(names, e.Name)
			}
		}
		recs = append(recs, "Verified hardware encoding is available: "+strings.Join(names, ", ")+
			". It offloads encoding from the CPU and is the better default when present.")
	} else {
		hasUnusable := false
		for _, e := range i.Encoders {
			if e.Backend != BackendSoftware && e.Verified && !e.Available {
				hasUnusable = true
				break
			}
		}
		if hasUnusable {
			recs = append(recs, "ffmpeg lists hardware encoders, but none initialized on this machine "+
				"(no usable GPU or driver). Encoding runs on the CPU with x264/x265.")
		} else {
			recs = append(recs, "No hardware video encoder detected. Encoding runs on the CPU with x264/x265.")
		}
	}

	switch {
	case i.LogicalCores <= 0:
		// Unknown core count, skip preset guidance.
	case i.LogicalCores < 4:
		recs = append(recs, "Low core count. Use x264 preset ultrafast or superfast and expect one 720p30 target.")
	case i.LogicalCores < 8:
		recs = append(recs, "Use x264 preset veryfast. A single 1080p30 to 1080p60 target is realistic.")
	case i.LogicalCores < 16:
		recs = append(recs, "Use x264 preset veryfast or faster. Several 1080p60 targets are realistic.")
	default:
		recs = append(recs, "High core count. Presets faster or fast are viable, with multiple 1080p60 targets in parallel.")
	}

	recs = append(recs, "Run the benchmark command for a measured capacity estimate on this machine.")
	return recs
}

// Report renders a human-readable doctor report.
func (i *Info) Report() string {
	var b strings.Builder
	line := func(k, v string) {
		if v == "" {
			v = "unknown"
		}
		fmt.Fprintf(&b, "  %-14s %s\n", k, v)
	}

	b.WriteString("System\n")
	line("cpu", i.CPUModel)
	line("cores", fmt.Sprintf("%d logical", i.LogicalCores))
	if i.MemTotalMB > 0 {
		line("memory", fmt.Sprintf("%d MB", i.MemTotalMB))
	} else {
		line("memory", "")
	}
	line("platform", i.OS+"/"+i.Arch)
	if i.FFmpegOK {
		line("ffmpeg", i.FFmpegVersion)
	} else {
		line("ffmpeg", "present but not runnable (broken or missing)")
	}

	b.WriteString("\nVideo encoders\n")
	if len(i.Encoders) == 0 {
		b.WriteString("  none detected (is ffmpeg installed and on PATH?)\n")
	} else {
		byCodec := map[string][]string{}
		var codecs []string
		for _, e := range i.Encoders {
			if _, seen := byCodec[e.Codec]; !seen {
				codecs = append(codecs, e.Codec)
			}
			tag := e.Name
			if e.Backend != BackendSoftware {
				if e.Available {
					tag += " (" + string(e.Backend) + ")"
				} else {
					tag += " (" + string(e.Backend) + ", unavailable)"
				}
			}
			byCodec[e.Codec] = append(byCodec[e.Codec], tag)
		}
		sort.Strings(codecs)
		for _, c := range codecs {
			fmt.Fprintf(&b, "  %-6s %s\n", c, strings.Join(byCodec[c], ", "))
		}
	}

	b.WriteString("\nHardware acceleration\n")
	if len(i.Hwaccels) == 0 {
		b.WriteString("  none\n")
	} else {
		fmt.Fprintf(&b, "  %s\n", strings.Join(i.Hwaccels, ", "))
	}

	b.WriteString("\nRecommendations\n")
	for _, r := range i.Recommendations() {
		fmt.Fprintf(&b, "  - %s\n", r)
	}
	return b.String()
}
