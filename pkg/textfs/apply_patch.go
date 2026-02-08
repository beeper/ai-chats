package textfs

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	beginPatchMarker         = "*** Begin Patch"
	endPatchMarker           = "*** End Patch"
	addFileMarker            = "*** Add File: "
	deleteFileMarker         = "*** Delete File: "
	updateFileMarker         = "*** Update File: "
	moveToMarker             = "*** Move to: "
	eofMarker                = "*** End of File"
	changeContextMarker      = "@@ "
	emptyChangeContextMarker = "@@"
)

type applyPatchHunk interface {
	isHunk()
}

type addFileHunk struct {
	path     string
	contents string
}

func (addFileHunk) isHunk() {}

type deleteFileHunk struct {
	path string
}

func (deleteFileHunk) isHunk() {}

type updateFileChunk struct {
	changeContext string
	hasContext    bool
	oldLines      []string
	newLines      []string
	isEndOfFile   bool
}

type updateFileHunk struct {
	path     string
	movePath string
	chunks   []updateFileChunk
}

func (updateFileHunk) isHunk() {}

type ApplyPatchSummary struct {
	Added    []string
	Modified []string
	Deleted  []string
}

type ApplyPatchResult struct {
	Summary ApplyPatchSummary
	Text    string
}

func ApplyPatch(ctx context.Context, store *Store, input string) (*ApplyPatchResult, error) {
	if store == nil {
		return nil, errors.New("store required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	parsed, err := parsePatchText(input)
	if err != nil {
		return nil, err
	}
	if len(parsed.hunks) == 0 {
		return nil, errors.New("no files were modified")
	}

	summary := ApplyPatchSummary{}
	seenAdded := map[string]struct{}{}
	seenModified := map[string]struct{}{}
	seenDeleted := map[string]struct{}{}
	record := func(bucket string, value string) {
		if strings.TrimSpace(value) == "" {
			return
		}
		switch bucket {
		case "added":
			if _, ok := seenAdded[value]; ok {
				return
			}
			seenAdded[value] = struct{}{}
			summary.Added = append(summary.Added, value)
		case "modified":
			if _, ok := seenModified[value]; ok {
				return
			}
			seenModified[value] = struct{}{}
			summary.Modified = append(summary.Modified, value)
		case "deleted":
			if _, ok := seenDeleted[value]; ok {
				return
			}
			seenDeleted[value] = struct{}{}
			summary.Deleted = append(summary.Deleted, value)
		}
	}

	for _, h := range parsed.hunks {
		switch hunk := h.(type) {
		case addFileHunk:
			path, err := NormalizePath(hunk.path)
			if err != nil {
				return nil, err
			}
			if _, err := store.Write(ctx, path, hunk.contents); err != nil {
				return nil, err
			}
			record("added", path)
		case deleteFileHunk:
			path, err := NormalizePath(hunk.path)
			if err != nil {
				return nil, err
			}
			if _, found, err := store.Read(ctx, path); err != nil {
				return nil, err
			} else if !found {
				return nil, fmt.Errorf("File not found: %s", path)
			}
			if err := store.Delete(ctx, path); err != nil {
				return nil, err
			}
			record("deleted", path)
		case updateFileHunk:
			path, err := NormalizePath(hunk.path)
			if err != nil {
				return nil, err
			}
			entry, found, err := store.Read(ctx, path)
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, fmt.Errorf("File not found: %s", path)
			}
			updated, err := applyUpdateHunks(entry.Content, hunk.chunks, path)
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(hunk.movePath) != "" {
				movePath, err := NormalizePath(hunk.movePath)
				if err != nil {
					return nil, err
				}
				if _, err := store.Write(ctx, movePath, updated); err != nil {
					return nil, err
				}
				if err := store.Delete(ctx, path); err != nil {
					return nil, err
				}
				record("modified", movePath)
			} else {
				if _, err := store.Write(ctx, path, updated); err != nil {
					return nil, err
				}
				record("modified", path)
			}
		default:
			return nil, errors.New("unsupported patch hunk")
		}
	}

	result := &ApplyPatchResult{Summary: summary}
	result.Text = formatPatchSummary(summary)
	return result, nil
}

type parsedPatch struct {
	hunks []applyPatchHunk
	patch string
}

func parsePatchText(input string) (*parsedPatch, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return nil, errors.New("invalid patch: input is empty")
	}
	normalized := strings.ReplaceAll(trimmed, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	validated, err := checkPatchBoundariesLenient(lines)
	if err != nil {
		return nil, err
	}
	if len(validated) < 2 {
		return nil, errors.New("invalid patch: empty")
	}
	lastLineIndex := len(validated) - 1
	remaining := validated[1:lastLineIndex]
	lineNumber := 2
	var hunks []applyPatchHunk
	for len(remaining) > 0 {
		hunk, consumed, err := parseOneHunk(remaining, lineNumber)
		if err != nil {
			return nil, err
		}
		hunks = append(hunks, hunk)
		lineNumber += consumed
		remaining = remaining[consumed:]
	}
	return &parsedPatch{hunks: hunks, patch: strings.Join(validated, "\n")}, nil
}

func checkPatchBoundariesLenient(lines []string) ([]string, error) {
	if err := checkPatchBoundariesStrict(lines); err == nil {
		return lines, nil
	}
	if len(lines) < 4 {
		return nil, checkPatchBoundariesStrict(lines)
	}
	first := strings.TrimSpace(lines[0])
	last := strings.TrimSpace(lines[len(lines)-1])
	if (first == "<<EOF" || first == "<<'EOF'" || first == "<<\"EOF\"") && strings.HasSuffix(last, "EOF") {
		inner := lines[1 : len(lines)-1]
		if err := checkPatchBoundariesStrict(inner); err == nil {
			return inner, nil
		}
		return nil, checkPatchBoundariesStrict(inner)
	}
	return nil, checkPatchBoundariesStrict(lines)
}

func checkPatchBoundariesStrict(lines []string) error {
	if len(lines) == 0 {
		return errors.New("invalid patch: input is empty")
	}
	first := strings.TrimSpace(lines[0])
	last := strings.TrimSpace(lines[len(lines)-1])
	if first == beginPatchMarker && last == endPatchMarker {
		return nil
	}
	if first != beginPatchMarker {
		return fmt.Errorf("the first line of the patch must be '%s'", beginPatchMarker)
	}
	return fmt.Errorf("the last line of the patch must be '%s'", endPatchMarker)
}

func parseOneHunk(lines []string, lineNumber int) (applyPatchHunk, int, error) {
	if len(lines) == 0 {
		return nil, 0, fmt.Errorf("invalid patch hunk at line %d: empty hunk", lineNumber)
	}
	firstLine := strings.TrimSpace(lines[0])
	if strings.HasPrefix(firstLine, addFileMarker) {
		targetPath := strings.TrimPrefix(firstLine, addFileMarker)
		contents := ""
		consumed := 1
		for _, addLine := range lines[1:] {
			if strings.HasPrefix(addLine, "+") {
				contents += addLine[1:] + "\n"
				consumed++
			} else {
				break
			}
		}
		return addFileHunk{path: targetPath, contents: contents}, consumed, nil
	}
	if strings.HasPrefix(firstLine, deleteFileMarker) {
		targetPath := strings.TrimPrefix(firstLine, deleteFileMarker)
		return deleteFileHunk{path: targetPath}, 1, nil
	}
	if strings.HasPrefix(firstLine, updateFileMarker) {
		targetPath := strings.TrimPrefix(firstLine, updateFileMarker)
		remaining := lines[1:]
		consumed := 1
		movePath := ""
		if len(remaining) > 0 {
			candidate := strings.TrimSpace(remaining[0])
			if strings.HasPrefix(candidate, moveToMarker) {
				movePath = strings.TrimPrefix(candidate, moveToMarker)
				remaining = remaining[1:]
				consumed++
			}
		}
		var chunks []updateFileChunk
		for len(remaining) > 0 {
			if strings.TrimSpace(remaining[0]) == "" {
				remaining = remaining[1:]
				consumed++
				continue
			}
			if strings.HasPrefix(remaining[0], "***") {
				break
			}
			chunk, used, err := parseUpdateFileChunk(remaining, lineNumber+consumed, len(chunks) == 0)
			if err != nil {
				return nil, 0, err
			}
			chunks = append(chunks, chunk)
			remaining = remaining[used:]
			consumed += used
		}
		if len(chunks) == 0 {
			return nil, 0, fmt.Errorf("invalid patch hunk at line %d: Update file hunk for path '%s' is empty", lineNumber, targetPath)
		}
		return updateFileHunk{path: targetPath, movePath: movePath, chunks: chunks}, consumed, nil
	}
	return nil, 0, fmt.Errorf("invalid patch hunk at line %d: '%s' is not a valid hunk header. Valid hunk headers: '%s{path}', '%s{path}', '%s{path}'", lineNumber, lines[0], addFileMarker, deleteFileMarker, updateFileMarker)
}

func parseUpdateFileChunk(lines []string, lineNumber int, allowMissingContext bool) (updateFileChunk, int, error) {
	if len(lines) == 0 {
		return updateFileChunk{}, 0, fmt.Errorf("invalid patch hunk at line %d: Update hunk does not contain any lines", lineNumber)
	}
	startIndex := 0
	chunk := updateFileChunk{}
	if lines[0] == emptyChangeContextMarker {
		startIndex = 1
	} else if strings.HasPrefix(lines[0], changeContextMarker) {
		chunk.changeContext = strings.TrimPrefix(lines[0], changeContextMarker)
		chunk.hasContext = true
		startIndex = 1
	} else if !allowMissingContext {
		return updateFileChunk{}, 0, fmt.Errorf("invalid patch hunk at line %d: Expected update hunk to start with a @@ context marker, got: '%s'", lineNumber, lines[0])
	}
	if startIndex >= len(lines) {
		return updateFileChunk{}, 0, fmt.Errorf("invalid patch hunk at line %d: Update hunk does not contain any lines", lineNumber+1)
	}
	parsedLines := 0
	for _, line := range lines[startIndex:] {
		if line == eofMarker {
			if parsedLines == 0 {
				return updateFileChunk{}, 0, fmt.Errorf("invalid patch hunk at line %d: Update hunk does not contain any lines", lineNumber+1)
			}
			chunk.isEndOfFile = true
			parsedLines++
			break
		}
		if line == "" {
			chunk.oldLines = append(chunk.oldLines, "")
			chunk.newLines = append(chunk.newLines, "")
			parsedLines++
			continue
		}
		marker := line[:1]
		switch marker {
		case " ":
			content := line[1:]
			chunk.oldLines = append(chunk.oldLines, content)
			chunk.newLines = append(chunk.newLines, content)
			parsedLines++
		case "+":
			chunk.newLines = append(chunk.newLines, line[1:])
			parsedLines++
		case "-":
			chunk.oldLines = append(chunk.oldLines, line[1:])
			parsedLines++
		default:
			if parsedLines == 0 {
				return updateFileChunk{}, 0, fmt.Errorf("invalid patch hunk at line %d: Unexpected line found in update hunk: '%s'. Every line should start with ' ' (context line), '+' (added line), or '-' (removed line)", lineNumber+1, line)
			}
			return chunk, parsedLines + startIndex, nil
		}
	}
	return chunk, parsedLines + startIndex, nil
}

func formatPatchSummary(summary ApplyPatchSummary) string {
	lines := []string{"Updated files:"}
	for _, file := range summary.Added {
		lines = append(lines, "A "+file)
	}
	for _, file := range summary.Modified {
		lines = append(lines, "M "+file)
	}
	for _, file := range summary.Deleted {
		lines = append(lines, "D "+file)
	}
	return strings.Join(lines, "\n")
}
