// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

package hardware

import "testing"

const sampleEncoders = `Encoders:
 V..... = Video
 A..... = Audio
 ------
 V....D libx264              libx264 H.264 / AVC / MPEG-4 AVC
 V....D libx265              libx265 H.265 / HEVC
 V....D h264_nvenc           NVIDIA NVENC H.264 encoder (codec h264)
 V....D hevc_nvenc           NVIDIA NVENC hevc encoder (codec hevc)
 V....D h264_qsv             H264 (Intel Quick Sync Video)
 V....D h264_vaapi           H.264/AVC (VAAPI)
 V....D libsvtav1            SVT-AV1 encoder
 A....D aac                  AAC (Advanced Audio Coding)
 V....D wrapped_avframe      AVFrame to AVPacket passthrough
`

func TestParseEncoders(t *testing.T) {
	got := parseEncoders(sampleEncoders)

	want := map[string]Backend{
		"libx264":    BackendSoftware,
		"libx265":    BackendSoftware,
		"h264_nvenc": BackendNVENC,
		"hevc_nvenc": BackendNVENC,
		"h264_qsv":   BackendQSV,
		"h264_vaapi": BackendVAAPI,
		"libsvtav1":  BackendSoftware,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d encoders, got %d: %+v", len(want), len(got), got)
	}
	for _, e := range got {
		wb, ok := want[e.Name]
		if !ok {
			t.Errorf("unexpected encoder %q", e.Name)
			continue
		}
		if e.Backend != wb {
			t.Errorf("%s: backend = %s, want %s", e.Name, e.Backend, wb)
		}
	}
}

func TestBestEncoderFor(t *testing.T) {
	i := &Info{Encoders: parseEncoders(sampleEncoders)}

	// Without verification, hardware encoders are not selectable; h264 falls to software.
	best, ok := i.BestEncoderFor("h264")
	if !ok || best.Backend != BackendSoftware {
		t.Fatalf("unverified h264 best = %+v, want software libx264", best)
	}

	// Mark the NVENC encoders as verified-available and re-check preference.
	for idx := range i.Encoders {
		if i.Encoders[idx].Backend == BackendNVENC {
			i.Encoders[idx].Available = true
			i.Encoders[idx].Verified = true
		}
	}
	best, ok = i.BestEncoderFor("h264")
	if !ok || best.Backend != BackendNVENC {
		t.Errorf("verified h264 best = %s (%s), want nvenc", best.Name, best.Backend)
	}

	// Software AV1 is always available.
	best, ok = i.BestEncoderFor("av1")
	if !ok || best.Name != "libsvtav1" {
		t.Errorf("av1 best = %+v, want libsvtav1", best)
	}

	if _, ok := i.BestEncoderFor("vp9"); ok {
		t.Error("did not expect a vp9 encoder in the sample")
	}
}

func TestDetectNeverPanics(t *testing.T) {
	info := Detect("definitely-not-a-real-ffmpeg-binary")
	if info == nil {
		t.Fatal("Detect returned nil")
	}
	if info.LogicalCores <= 0 {
		t.Error("expected a positive core count")
	}
}
