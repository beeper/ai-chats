package textfs

import (
	"fmt"
	"strings"
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
	lines := strings.Split(content, "\n")
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
	firstLineBytes := len(lines[0])
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
		lineBytes := len(line)
		if i > 0 {
			lineBytes++
		}
		if outputBytes+lineBytes > maxBytes {
			truncatedBy = "bytes"
			break
		}
		outputLines = append(outputLines, line)
		outputBytes += lineBytes
	}
	outputContent := strings.Join(outputLines, "\n")
	return Truncation{
		Content:     outputContent,
		Truncated:   true,
		TruncatedBy: truncatedBy,
		TotalLines:  totalLines,
		TotalBytes:  totalBytes,
		OutputLines: len(outputLines),
		OutputBytes: len(outputContent),
		MaxLines:    maxLines,
		MaxBytes:    maxBytes,
	}
}
