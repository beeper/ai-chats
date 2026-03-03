package memory

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strings"
)

type Chunk struct {
	StartLine int
	EndLine   int
	Text      string
	Hash      string
}

func ChunkMarkdown(content string, tokens, overlap int) []Chunk {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return nil
	}
	maxChars := tokens * 4
	if maxChars < 32 {
		maxChars = 32
	}
	overlapChars := overlap * 4
	if overlapChars < 0 {
		overlapChars = 0
	}

	type lineEntry struct {
		line   string
		lineNo int
	}

	var chunks []Chunk
	var current []lineEntry
	currentChars := 0

	flush := func() {
		if len(current) == 0 {
			return
		}
		start := current[0].lineNo
		end := current[len(current)-1].lineNo
		var b strings.Builder
		for i, entry := range current {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(entry.line)
		}
		text := b.String()
		chunks = append(chunks, Chunk{
			StartLine: start,
			EndLine:   end,
			Text:      text,
			Hash:      hashText(text),
		})
	}

	carryOverlap := func() {
		if overlapChars <= 0 || len(current) == 0 {
			current = nil
			currentChars = 0
			return
		}
		chars := 0
		start := len(current)
		for i := len(current) - 1; i >= 0; i-- {
			chars += len(current[i].line) + 1
			start = i
			if chars >= overlapChars {
				break
			}
		}
		current = slices.Clone(current[start:])
		currentChars = chars
	}

	for i, line := range lines {
		lineNo := i + 1
		segments := splitLineSegments(line, maxChars)
		for _, segment := range segments {
			lineSize := len(segment) + 1
			if currentChars+lineSize > maxChars && len(current) > 0 {
				flush()
				carryOverlap()
			}
			current = append(current, lineEntry{line: segment, lineNo: lineNo})
			currentChars += lineSize
		}
	}
	flush()
	return chunks
}

func splitLineSegments(line string, maxChars int) []string {
	if line == "" {
		return []string{""}
	}
	if maxChars <= 0 {
		return []string{line}
	}
	var segments []string
	for start := 0; start < len(line); start += maxChars {
		end := start + maxChars
		if end > len(line) {
			end = len(line)
		}
		segments = append(segments, line[start:end])
	}
	return segments
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
