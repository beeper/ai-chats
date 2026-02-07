package agents

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"

	"github.com/beeper/ai-bridge/pkg/textfs"
)

const (
	DefaultBootstrapMaxChars = 20_000
	bootstrapHeadRatio       = 0.7
	bootstrapTailRatio       = 0.2
)

const (
	DefaultAgentsFilename    = "AGENTS.md"
	DefaultSoulFilename      = "SOUL.md"
	DefaultToolsFilename     = "TOOLS.md"
	DefaultIdentityFilename  = "IDENTITY.md"
	DefaultUserFilename      = "USER.md"
	DefaultHeartbeatFilename = "HEARTBEAT.md"
	DefaultBootstrapFilename = "BOOTSTRAP.md"
	DefaultMemoryFilename    = "MEMORY.md"
	DefaultMemoryAltFilename = "memory.md"
)

var coreBootstrapFiles = []string{
	DefaultAgentsFilename,
	DefaultSoulFilename,
	DefaultToolsFilename,
	DefaultIdentityFilename,
	DefaultUserFilename,
	DefaultHeartbeatFilename,
}

var baseBootstrapFiles = []string{
	DefaultAgentsFilename,
	DefaultSoulFilename,
	DefaultToolsFilename,
	DefaultIdentityFilename,
	DefaultUserFilename,
	DefaultHeartbeatFilename,
}

var subagentBootstrapAllowlist = map[string]struct{}{
	DefaultAgentsFilename: {},
	DefaultToolsFilename:  {},
}

// WorkspaceBootstrapFile represents a bootstrapped workspace file.
type WorkspaceBootstrapFile struct {
	Name    string
	Path    string
	Content string
	Missing bool
}

// EnsureBootstrapFiles ensures the default workspace files exist in the virtual FS.
// Returns true if this looks like a brand-new workspace.
func EnsureBootstrapFiles(ctx context.Context, store *textfs.Store) (bool, error) {
	if store == nil {
		return false, errors.New("textfs store is required")
	}
	brandNew := true
	for _, name := range coreBootstrapFiles {
		_, found, err := store.Read(ctx, name)
		if err != nil {
			return false, err
		}
		if found {
			brandNew = false
		}
	}

	for _, name := range coreBootstrapFiles {
		content, err := loadWorkspaceTemplate(name)
		if err != nil {
			return brandNew, err
		}
		if _, err := store.WriteIfMissing(ctx, name, content); err != nil {
			return brandNew, err
		}
	}

	if brandNew {
		content, err := loadWorkspaceTemplate(DefaultBootstrapFilename)
		if err != nil {
			return brandNew, err
		}
		if _, err := store.WriteIfMissing(ctx, DefaultBootstrapFilename, content); err != nil {
			return brandNew, err
		}
	}

	return brandNew, nil
}

// LoadBootstrapFiles loads the default workspace files from the virtual FS.
func LoadBootstrapFiles(ctx context.Context, store *textfs.Store) ([]WorkspaceBootstrapFile, error) {
	if store == nil {
		return nil, errors.New("textfs store is required")
	}
	files := make([]WorkspaceBootstrapFile, 0, len(baseBootstrapFiles)+2)
	for _, name := range baseBootstrapFiles {
		entry, found, err := store.Read(ctx, name)
		if err != nil {
			return nil, err
		}
		file := WorkspaceBootstrapFile{Name: name, Path: name, Missing: !found}
		if found && entry != nil {
			file.Content = entry.Content
		}
		files = append(files, file)
	}

	// BOOTSTRAP.md is meant to be deleted after first-run. Treat it as optional:
	// include it only when it exists so we don't inject a perpetual [MISSING] stub.
	if entry, found, err := store.Read(ctx, DefaultBootstrapFilename); err != nil {
		return nil, err
	} else if found && entry != nil {
		files = append(files, WorkspaceBootstrapFile{
			Name:    DefaultBootstrapFilename,
			Path:    DefaultBootstrapFilename,
			Content: entry.Content,
			Missing: false,
		})
	}

	memoryEntries, err := loadMemoryBootstrapEntries(ctx, store)
	if err != nil {
		return nil, err
	}
	files = append(files, memoryEntries...)
	return files, nil
}

func loadMemoryBootstrapEntries(ctx context.Context, store *textfs.Store) ([]WorkspaceBootstrapFile, error) {
	entries := []WorkspaceBootstrapFile{}
	for _, name := range []string{DefaultMemoryFilename, DefaultMemoryAltFilename} {
		entry, found, err := store.Read(ctx, name)
		if err != nil {
			return nil, err
		}
		if !found || entry == nil {
			continue
		}
		entries = append(entries, WorkspaceBootstrapFile{
			Name:    name,
			Path:    name,
			Content: entry.Content,
			Missing: false,
		})
	}
	return entries, nil
}

// FilterBootstrapFilesForSession filters bootstrap files for subagent sessions.
func FilterBootstrapFilesForSession(files []WorkspaceBootstrapFile, isSubagent bool) []WorkspaceBootstrapFile {
	if !isSubagent {
		return files
	}
	filtered := make([]WorkspaceBootstrapFile, 0, len(files))
	for _, file := range files {
		if _, ok := subagentBootstrapAllowlist[file.Name]; ok {
			filtered = append(filtered, file)
		}
	}
	return filtered
}

type TrimBootstrapResult struct {
	Content        string
	Truncated      bool
	MaxChars       int
	OriginalLength int
}

// TrimBootstrapContent trims a file's content to the configured max chars.
func TrimBootstrapContent(content, fileName string, maxChars int) TrimBootstrapResult {
	if maxChars <= 0 {
		maxChars = DefaultBootstrapMaxChars
	}
	trimmed := strings.TrimRightFunc(content, unicode.IsSpace)
	if len(trimmed) <= maxChars {
		return TrimBootstrapResult{
			Content:        trimmed,
			Truncated:      false,
			MaxChars:       maxChars,
			OriginalLength: len(trimmed),
		}
	}

	headChars := int(math.Floor(float64(maxChars) * bootstrapHeadRatio))
	tailChars := int(math.Floor(float64(maxChars) * bootstrapTailRatio))
	head := trimmed[:headChars]
	tail := trimmed[len(trimmed)-tailChars:]

	marker := strings.Join([]string{
		"",
		fmt.Sprintf("[...truncated, read %s for full content...]", fileName),
		fmt.Sprintf("…(truncated %s: kept %d+%d chars of %d)…", fileName, headChars, tailChars, len(trimmed)),
		"",
	}, "\n")
	contentWithMarker := strings.Join([]string{head, marker, tail}, "\n")
	return TrimBootstrapResult{
		Content:        contentWithMarker,
		Truncated:      true,
		MaxChars:       maxChars,
		OriginalLength: len(trimmed),
	}
}

// BuildBootstrapContextFiles prepares the injected context files for the system prompt.
func BuildBootstrapContextFiles(
	files []WorkspaceBootstrapFile,
	maxChars int,
	warn func(message string),
) []EmbeddedContextFile {
	if maxChars <= 0 {
		maxChars = DefaultBootstrapMaxChars
	}
	result := make([]EmbeddedContextFile, 0, len(files))
	for _, file := range files {
		if file.Missing {
			result = append(result, EmbeddedContextFile{
				Path:    file.Name,
				Content: fmt.Sprintf("[MISSING] Expected at: %s", file.Path),
			})
			continue
		}
		trimmed := TrimBootstrapContent(file.Content, file.Name, maxChars)
		if strings.TrimSpace(trimmed.Content) == "" {
			continue
		}
		if trimmed.Truncated && warn != nil {
			warn(fmt.Sprintf(
				"workspace bootstrap file %s is %d chars (limit %d); truncating in injected context",
				file.Name,
				trimmed.OriginalLength,
				trimmed.MaxChars,
			))
		}
		result = append(result, EmbeddedContextFile{
			Path:    file.Name,
			Content: trimmed.Content,
		})
	}
	return result
}
