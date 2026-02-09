package textfs

import (
	"path"
	"slices"
	"strings"
)

const noteMaxBytesDefault = 128 * 1024

var allowedNoteExts = []string{
	".adoc",
	".asciidoc",
	".csv",
	".log",
	".markdown",
	".md",
	".mdx",
	".org",
	".rst",
	".text",
	".txt",
}

// AllowedNoteExtensions returns the hardcoded list of indexable "note" file extensions.
// Extensions are lowercase and include a leading dot.
func AllowedNoteExtensions() []string {
	out := slices.Clone(allowedNoteExts)
	slices.Sort(out)
	return out
}

func formatAllowedNoteExtensions() string {
	return strings.Join(AllowedNoteExtensions(), ", ")
}

// IsAllowedTextNotePath checks whether a virtual path is allowed for note indexing/reading.
// It requires an explicit file extension in the allowlist.
func IsAllowedTextNotePath(relPath string) (ok bool, ext string, reason string) {
	normalized := strings.TrimSpace(relPath)
	if normalized == "" {
		return false, "", "empty_path"
	}
	normalized = strings.ReplaceAll(normalized, "\\", "/")
	normalized = strings.TrimPrefix(normalized, "./")
	normalized = strings.TrimLeft(normalized, "/")

	ext = strings.ToLower(path.Ext(normalized))
	if ext == "" {
		return false, "", "missing_extension"
	}
	for _, allowed := range allowedNoteExts {
		if ext == allowed {
			return true, ext, ""
		}
	}
	return false, ext, "unsupported_extension"
}

// NoteMaxBytesDefault is the default per-file size cap for note indexing and reads.
func NoteMaxBytesDefault() int {
	return noteMaxBytesDefault
}
