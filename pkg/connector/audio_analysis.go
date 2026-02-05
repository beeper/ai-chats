package connector

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"sync"
)

const waveformPoints = 100

var (
	ffmpegOnce      sync.Once
	ffmpegAvailable bool
)

func analyzeAudio(data []byte, mimeType string) (int, []int) {
	if len(data) == 0 {
		return 0, nil
	}

	normalized := strings.ToLower(normalizeMimeString(mimeType))
	switch normalized {
	case "audio/wav", "audio/x-wav", "audio/wave":
		if samples, sampleRate, err := parseWavPCM(data); err == nil {
			return buildWaveform(samples, sampleRate)
		}
	case "audio/aiff", "audio/x-aiff":
		if samples, sampleRate, err := parseAiffPCM(data); err == nil {
			return buildWaveform(samples, sampleRate)
		}
	}

	if hasFFmpeg() {
		if samples, sampleRate, err := decodePCMWithFFmpeg(data); err == nil {
			return buildWaveform(samples, sampleRate)
		}
	}

	return 0, nil
}

func hasFFmpeg() bool {
	ffmpegOnce.Do(func() {
		if _, err := exec.LookPath("ffmpeg"); err == nil {
			ffmpegAvailable = true
		}
	})
	return ffmpegAvailable
}

func decodePCMWithFFmpeg(data []byte) ([]int16, int, error) {
	tmpFile, err := os.CreateTemp("", "tts-audio-*")
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create temp audio file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return nil, 0, fmt.Errorf("failed to write temp audio file: %w", err)
	}
	tmpFile.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-i", tmpPath, "-ac", "1", "-ar", "8000", "-f", "s16le", "-")
	out, err := cmd.Output()
	if err != nil {
		return nil, 0, err
	}
	if len(out) < 2 {
		return nil, 0, fmt.Errorf("ffmpeg returned no PCM data")
	}

	samples := bytesToInt16LE(out)
	return samples, 8000, nil
}

func buildWaveform(samples []int16, sampleRate int) (int, []int) {
	if sampleRate <= 0 || len(samples) == 0 {
		return 0, nil
	}

	durationMs := int(math.Round(float64(len(samples)) / float64(sampleRate) * 1000))
	points := waveformPoints
	if len(samples) < points {
		points = len(samples)
	}
	if points == 0 {
		return durationMs, nil
	}

	waveform := make([]int, points)
	bucketSize := len(samples) / points
	if bucketSize == 0 {
		bucketSize = 1
	}

	for i := 0; i < points; i++ {
		start := i * bucketSize
		end := start + bucketSize
		if i == points-1 || end > len(samples) {
			end = len(samples)
		}

		maxAmp := 0
		for j := start; j < end; j++ {
			amp := int(samples[j])
			if amp < 0 {
				amp = -amp
			}
			if amp > maxAmp {
				maxAmp = amp
			}
		}

		level := int(math.Round(float64(maxAmp) / 32767.0 * 255.0))
		if level < 0 {
			level = 0
		} else if level > 255 {
			level = 255
		}
		waveform[i] = level
	}

	return durationMs, waveform
}

func parseWavPCM(data []byte) ([]int16, int, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, 0, fmt.Errorf("not a WAV file")
	}

	var (
		channels      uint16
		sampleRate    uint32
		bitsPerSample uint16
		audioFormat   uint16
		dataChunk     []byte
	)

	offset := 12
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if offset+chunkSize > len(data) {
			break
		}

		switch chunkID {
		case "fmt ":
			if chunkSize < 16 {
				return nil, 0, fmt.Errorf("invalid wav fmt chunk")
			}
			audioFormat = binary.LittleEndian.Uint16(data[offset : offset+2])
			channels = binary.LittleEndian.Uint16(data[offset+2 : offset+4])
			sampleRate = binary.LittleEndian.Uint32(data[offset+4 : offset+8])
			bitsPerSample = binary.LittleEndian.Uint16(data[offset+14 : offset+16])
		case "data":
			dataChunk = data[offset : offset+chunkSize]
		}

		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}

	if dataChunk == nil || channels == 0 || sampleRate == 0 {
		return nil, 0, fmt.Errorf("missing wav data")
	}
	if audioFormat != 1 && audioFormat != 3 {
		return nil, 0, fmt.Errorf("unsupported wav format")
	}

	switch bitsPerSample {
	case 8:
		return pcm8ToInt16(dataChunk, int(channels)), int(sampleRate), nil
	case 16:
		return pcm16leToInt16(dataChunk, int(channels)), int(sampleRate), nil
	default:
		return nil, 0, fmt.Errorf("unsupported wav bit depth")
	}
}

func parseAiffPCM(data []byte) ([]int16, int, error) {
	if len(data) < 12 || string(data[0:4]) != "FORM" {
		return nil, 0, fmt.Errorf("not an AIFF file")
	}
	formType := string(data[8:12])
	if formType != "AIFF" && formType != "AIFC" {
		return nil, 0, fmt.Errorf("unsupported AIFF type")
	}

	var (
		channels   uint16
		sampleRate float64
		sampleSize uint16
		dataChunk  []byte
	)

	offset := 12
	for offset+8 <= len(data) {
		chunkID := string(data[offset : offset+4])
		chunkSize := int(binary.BigEndian.Uint32(data[offset+4 : offset+8]))
		offset += 8
		if offset+chunkSize > len(data) {
			break
		}

		switch chunkID {
		case "COMM":
			if chunkSize < 18 {
				return nil, 0, fmt.Errorf("invalid AIFF COMM chunk")
			}
			channels = binary.BigEndian.Uint16(data[offset : offset+2])
			sampleSize = binary.BigEndian.Uint16(data[offset+6 : offset+8])
			sampleRate = parseExtended80(data[offset+8 : offset+18])
		case "SSND":
			if chunkSize < 8 {
				return nil, 0, fmt.Errorf("invalid AIFF SSND chunk")
			}
			dataOffset := int(binary.BigEndian.Uint32(data[offset : offset+4]))
			start := offset + 8 + dataOffset
			if start > offset+chunkSize {
				return nil, 0, fmt.Errorf("invalid AIFF SSND offset")
			}
			dataChunk = data[start : offset+chunkSize]
		}

		offset += chunkSize
		if chunkSize%2 == 1 {
			offset++
		}
	}

	if dataChunk == nil || channels == 0 || sampleRate <= 0 {
		return nil, 0, fmt.Errorf("missing AIFF data")
	}

	switch sampleSize {
	case 8:
		return pcm8SignedToInt16(dataChunk, int(channels)), int(sampleRate), nil
	case 16:
		return pcm16beToInt16(dataChunk, int(channels)), int(sampleRate), nil
	default:
		return nil, 0, fmt.Errorf("unsupported AIFF bit depth")
	}
}

func parseExtended80(b []byte) float64 {
	if len(b) < 10 {
		return 0
	}
	sign := b[0] & 0x80
	exp := int(b[0]&0x7f)<<8 | int(b[1])
	mant := uint64(b[2])<<56 | uint64(b[3])<<48 | uint64(b[4])<<40 | uint64(b[5])<<32 |
		uint64(b[6])<<24 | uint64(b[7])<<16 | uint64(b[8])<<8 | uint64(b[9])
	if exp == 0 && mant == 0 {
		return 0
	}
	exp -= 16383
	val := float64(mant) / float64(uint64(1)<<63)
	if sign != 0 {
		val = -val
	}
	return math.Ldexp(val, exp)
}

func pcm16leToInt16(data []byte, channels int) []int16 {
	samples := bytesToInt16LE(data)
	return monoFromInterleaved(samples, channels)
}

func pcm16beToInt16(data []byte, channels int) []int16 {
	if len(data) < 2 {
		return nil
	}
	count := len(data) / 2
	samples := make([]int16, count)
	for i := 0; i < count; i++ {
		samples[i] = int16(binary.BigEndian.Uint16(data[i*2 : i*2+2]))
	}
	return monoFromInterleaved(samples, channels)
}

func pcm8ToInt16(data []byte, channels int) []int16 {
	if len(data) == 0 {
		return nil
	}
	samples := make([]int16, len(data))
	for i, b := range data {
		samples[i] = int16(int(b)-128) << 8
	}
	return monoFromInterleaved(samples, channels)
}

func pcm8SignedToInt16(data []byte, channels int) []int16 {
	if len(data) == 0 {
		return nil
	}
	samples := make([]int16, len(data))
	for i, b := range data {
		samples[i] = int16(int8(b)) << 8
	}
	return monoFromInterleaved(samples, channels)
}

func bytesToInt16LE(data []byte) []int16 {
	if len(data) < 2 {
		return nil
	}
	count := len(data) / 2
	samples := make([]int16, count)
	reader := bytes.NewReader(data[:count*2])
	_ = binary.Read(reader, binary.LittleEndian, &samples)
	return samples
}

func monoFromInterleaved(samples []int16, channels int) []int16 {
	if channels <= 1 || len(samples) == 0 {
		return samples
	}
	frames := len(samples) / channels
	if frames == 0 {
		return nil
	}
	mono := make([]int16, frames)
	for i := 0; i < frames; i++ {
		idx := i * channels
		mono[i] = samples[idx]
	}
	return mono
}
