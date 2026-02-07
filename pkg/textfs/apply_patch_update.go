package textfs

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
)

type replacement struct {
	start    int
	oldLen   int
	newLines []string
}

func applyUpdateHunks(original string, chunks []updateFileChunk, filePath string) (string, error) {
	originalLines := strings.Split(strings.ReplaceAll(original, "\r\n", "\n"), "\n")
	if len(originalLines) > 0 && originalLines[len(originalLines)-1] == "" {
		originalLines = originalLines[:len(originalLines)-1]
	}
	replacements, err := computeReplacements(originalLines, filePath, chunks)
	if err != nil {
		return "", err
	}
	newLines := applyReplacements(originalLines, replacements)
	if len(newLines) == 0 || newLines[len(newLines)-1] != "" {
		newLines = append(newLines, "")
	}
	return strings.Join(newLines, "\n"), nil
}

func computeReplacements(originalLines []string, filePath string, chunks []updateFileChunk) ([]replacement, error) {
	var replacements []replacement
	lineIndex := 0
	for _, chunk := range chunks {
		if chunk.hasContext {
			ctxIndex := seekSequence(originalLines, []string{chunk.changeContext}, lineIndex, false)
			if ctxIndex == nil {
				return nil, fmt.Errorf("failed to find context '%s' in %s", chunk.changeContext, filePath)
			}
			lineIndex = *ctxIndex + 1
		}
		if len(chunk.oldLines) == 0 {
			insertionIndex := len(originalLines)
			if len(originalLines) > 0 && originalLines[len(originalLines)-1] == "" {
				insertionIndex = len(originalLines) - 1
			}
			replacements = append(replacements, replacement{start: insertionIndex, oldLen: 0, newLines: chunk.newLines})
			continue
		}
		pattern := slices.Clone(chunk.oldLines)
		newSlice := slices.Clone(chunk.newLines)
		found := seekSequence(originalLines, pattern, lineIndex, chunk.isEndOfFile)
		if found == nil && len(pattern) > 0 && pattern[len(pattern)-1] == "" {
			pattern = pattern[:len(pattern)-1]
			if len(newSlice) > 0 && newSlice[len(newSlice)-1] == "" {
				newSlice = newSlice[:len(newSlice)-1]
			}
			found = seekSequence(originalLines, pattern, lineIndex, chunk.isEndOfFile)
		}
		if found == nil {
			return nil, fmt.Errorf("failed to find expected lines in %s:\n%s", filePath, strings.Join(chunk.oldLines, "\n"))
		}
		replacements = append(replacements, replacement{start: *found, oldLen: len(pattern), newLines: newSlice})
		lineIndex = *found + len(pattern)
	}
	sortReplacements(replacements)
	return replacements, nil
}

func sortReplacements(replacements []replacement) {
	if len(replacements) < 2 {
		return
	}
	for i := 0; i < len(replacements)-1; i++ {
		for j := i + 1; j < len(replacements); j++ {
			if replacements[j].start < replacements[i].start {
				replacements[i], replacements[j] = replacements[j], replacements[i]
			}
		}
	}
}

func applyReplacements(lines []string, replacements []replacement) []string {
	result := slices.Clone(lines)
	for i := len(replacements) - 1; i >= 0; i-- {
		rep := replacements[i]
		start := rep.start
		for j := 0; j < rep.oldLen; j++ {
			if start < len(result) {
				result = append(result[:start], result[start+1:]...)
			}
		}
		if len(rep.newLines) > 0 {
			before := slices.Clone(result[:start])
			after := slices.Clone(result[start:])
			result = append(before, append(rep.newLines, after...)...)
		}
	}
	return result
}

func seekSequence(lines []string, pattern []string, start int, eof bool) *int {
	if len(pattern) == 0 {
		idx := start
		return &idx
	}
	if len(pattern) > len(lines) {
		return nil
	}
	maxStart := len(lines) - len(pattern)
	searchStart := start
	if eof && len(lines) >= len(pattern) {
		searchStart = maxStart
	}
	if searchStart > maxStart {
		return nil
	}
	if idx := seekSequenceWithNormalize(lines, pattern, searchStart, maxStart, func(v string) string { return v }); idx != nil {
		return idx
	}
	if idx := seekSequenceWithNormalize(lines, pattern, searchStart, maxStart, func(v string) string {
		return strings.TrimRightFunc(v, unicode.IsSpace)
	}); idx != nil {
		return idx
	}
	if idx := seekSequenceWithNormalize(lines, pattern, searchStart, maxStart, strings.TrimSpace); idx != nil {
		return idx
	}
	return seekSequenceWithNormalize(lines, pattern, searchStart, maxStart, func(v string) string {
		return normalizePunctuation(strings.TrimSpace(v))
	})
}

func seekSequenceWithNormalize(lines []string, pattern []string, start int, maxStart int, normalize func(string) string) *int {
	for i := start; i <= maxStart; i++ {
		if linesMatch(lines, pattern, i, normalize) {
			idx := i
			return &idx
		}
	}
	return nil
}

func linesMatch(lines []string, pattern []string, start int, normalize func(string) string) bool {
	for idx := 0; idx < len(pattern); idx++ {
		if normalize(lines[start+idx]) != normalize(pattern[idx]) {
			return false
		}
	}
	return true
}

func normalizePunctuation(value string) string {
	var b strings.Builder
	for _, r := range value {
		switch r {
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			b.WriteRune('-')
		case '\u2018', '\u2019', '\u201A', '\u201B':
			b.WriteRune('\'')
		case '\u201C', '\u201D', '\u201E', '\u201F':
			b.WriteRune('"')
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008', '\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
