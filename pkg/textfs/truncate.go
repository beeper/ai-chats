package textfs

import (
	"fmt"
)

const (
	DefaultMaxLines   = 2000
	DefaultMaxBytes   = 50 * 1024
	GrepMaxLineLength = 500
)

type Truncation struct {
	Content               string
	Truncated             bool
	TruncatedBy           string
	TotalLines            int
	TotalBytes            int
	OutputLines           int
	OutputBytes           int
	FirstLineExceedsLimit bool
	MaxLines              int
	MaxBytes              int
}

func FormatSize(bytes int) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

// TruncateHead keeps the first maxLines/maxBytes of content.
func TruncateHead(content string, maxLines, maxBytes int) Truncation {
	if maxLines <= 0 {
		maxLines = DefaultMaxLines
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	lines := splitLines(content)
	totalLines := len(lines)
	totalBytes := len(content)
	if totalLines <= maxLines && totalBytes <= maxBytes {
		return Truncation{
			Content:     content,
			Truncated:   false,
			TotalLines:  totalLines,
			TotalBytes:  totalBytes,
			OutputLines: totalLines,
			OutputBytes: totalBytes,
			MaxLines:    maxLines,
			MaxBytes:    maxBytes,
		}
	}
	firstLineBytes := len([]byte(lines[0]))
	if firstLineBytes > maxBytes {
		return Truncation{
			Content:               "",
			Truncated:             true,
			TruncatedBy:           "bytes",
			TotalLines:            totalLines,
			TotalBytes:            totalBytes,
			OutputLines:           0,
			OutputBytes:           0,
			FirstLineExceedsLimit: true,
			MaxLines:              maxLines,
			MaxBytes:              maxBytes,
		}
	}
	outputLines := make([]string, 0, maxLines)
	outputBytes := 0
	truncatedBy := "lines"
	for i := 0; i < len(lines) && i < maxLines; i++ {
		line := lines[i]
		lineBytes := len([]byte(line))
		if i > 0 {
			lineBytes += 1
		}
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		outputLines = append(outputLines, line)
		outputBytes += lineBytes
	}
	outputContent := joinLines(outputLines)
	return Truncation{
		Content:     outputContent,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(outputLines),
		OutputBytes: len([]byte(outputContent)),
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}

func splitLines(content string) []string {
	if content == "" {
		return []string{""}
	}
	out := make([]string, 0, 64)
	start := 0
	for i := 0; i < len(content); i++ {
		if content[i] == '\n' {
			out = append(out, content[start:i])
			start = i + 1
		}
	}
	out = append(out, content[start:])
	return out
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	total := 0
	for _, line := range lines {
		total += len(line) + 1
	}
	b := make([]byte, 0, total)
	for i, line := range lines {
		if i > 0 {
			b = append(b, '\n')
		}
		b = append(b, line...)
	}
	return string(b)
}
