package ai

import (
	"context"
	"math"
	"os"

	"go.mau.fi/util/ffmpeg"
)

// analyzeVideo returns width, height, and duration (ms) of a video.
// Returns zeros if ffprobe is unavailable or the data can't be probed.
func analyzeVideo(ctx context.Context, data []byte) (width, height, durationMs int) {
	if len(data) == 0 || !ffmpeg.ProbeSupported() {
		return 0, 0, 0
	}
	tmp, err := os.CreateTemp("", "video-probe-*")
	if err != nil {
		return 0, 0, 0
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return 0, 0, 0
	}
	tmp.Close()

	result, err := ffmpeg.Probe(ctx, tmp.Name())
	if err != nil || result == nil {
		return 0, 0, 0
	}
	for _, s := range result.Streams {
		if s.CodecType == "video" {
			dur := s.Duration
			if dur <= 0 && result.Format != nil {
				dur = result.Format.Duration
			}
			return s.Width, s.Height, int(math.Round(dur * 1000))
		}
	}
	return 0, 0, 0
}
