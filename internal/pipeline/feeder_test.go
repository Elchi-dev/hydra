// Copyright (c) 2026 Elchi. All rights reserved.
// Made by Elchi. Licensed under the Hydra Source-Available License; see LICENSE.

package pipeline

import (
	"bytes"
	"testing"

	"github.com/yutopp/go-flv"
	flvtag "github.com/yutopp/go-flv/tag"
)

// nopCloser makes a bytes.Buffer satisfy io.WriteCloser.
type nopCloser struct{ *bytes.Buffer }

func (nopCloser) Close() error { return nil }

func vtag(ts uint32) *flvtag.FlvTag {
	return &flvtag.FlvTag{
		TagType:   flvtag.TagTypeVideo,
		Timestamp: ts,
		Data: &flvtag.VideoData{
			FrameType:     flvtag.FrameTypeKeyFrame,
			CodecID:       flvtag.CodecIDAVC,
			AVCPacketType: flvtag.AVCPacketTypeNALU,
			Data:          bytes.NewReader([]byte{0x00, 0x00, 0x00, 0x01, 0x09, 0x10}),
		},
	}
}

// TestFeederTimestampContinuity is the core BRB-seam guarantee: when the source
// switches (live -> BRB), whose timestamps restart near zero, the feeder must
// keep emitting strictly increasing timestamps so ffmpeg never rewinds and the
// platform connections stay alive.
func TestFeederTimestampContinuity(t *testing.T) {
	buf := nopCloser{new(bytes.Buffer)}
	f, err := NewFeeder(buf)
	if err != nil {
		t.Fatal(err)
	}

	// Live source: timestamps climbing to ~1000ms.
	for _, ts := range []uint32{0, 33, 66, 100, 1000} {
		if err := f.Write(SourceLive, vtag(ts)); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate OBS drop -> cut to BRB, whose timestamps restart at 0.
	f.Activate(SourceBRB)
	for _, ts := range []uint32{0, 33, 66, 100} {
		if err := f.Write(SourceBRB, vtag(ts)); err != nil {
			t.Fatal(err)
		}
	}
	// A late live tag (from a lingering goroutine) must be dropped, not emitted.
	if err := f.Write(SourceLive, vtag(50)); err != nil {
		t.Fatal(err)
	}

	// Decode the produced FLV and assert timestamps strictly increase.
	dec, err := flv.NewDecoder(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	var prev int64 = -1
	count := 0
	for {
		var tag flvtag.FlvTag
		if err := dec.Decode(&tag); err != nil {
			break
		}
		ts := int64(tag.Timestamp)
		if ts <= prev {
			t.Fatalf("timestamp not strictly increasing: %d after %d", ts, prev)
		}
		prev = ts
		count++
		tag.Close()
	}
	// 5 live + 4 BRB = 9 emitted; the late live tag must have been dropped.
	if count != 9 {
		t.Fatalf("expected 9 emitted tags (late live tag dropped), got %d", count)
	}
	t.Logf("continuity ok, %d tags, last ts=%dms, monotonic across switch", count, prev)
}
