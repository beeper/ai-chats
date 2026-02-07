package connector

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

func (oc *AIClient) buildBootstrapContextFiles(ctx context.Context, agentID string, meta *PortalMetadata) []agents.EmbeddedContextFile {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return nil
	}
	if strings.TrimSpace(agentID) == "" {
		agentID = "default"
	}
	store := textfs.NewStore(
		oc.UserLogin.Bridge.DB.Database,
		string(oc.UserLogin.Bridge.DB.BridgeID),
		string(oc.UserLogin.ID),
		agentID,
	)

	skipBootstrap := false
	if oc.connector != nil && oc.connector.Config.Agents != nil && oc.connector.Config.Agents.Defaults != nil {
		skipBootstrap = oc.connector.Config.Agents.Defaults.SkipBootstrap
	}
	if !skipBootstrap {
		if _, err := agents.EnsureBootstrapFiles(ctx, store); err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("failed to ensure workspace bootstrap files")
		}
	}

	// Auto-cleanup BOOTSTRAP.md once the workspace is no longer "first run".
	// This prevents stale bootstrap instructions and avoids injecting missing placeholders.
	oc.maybeAutoDeleteBootstrap(ctx, store)

	files, err := agents.LoadBootstrapFiles(ctx, store)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("failed to load workspace bootstrap files")
		return nil
	}
	if meta != nil && strings.TrimSpace(meta.SubagentParentRoomID) != "" {
		files = agents.FilterBootstrapFilesForSession(files, true)
	}

	maxChars := agents.DefaultBootstrapMaxChars
	if oc.connector != nil && oc.connector.Config.Agents != nil && oc.connector.Config.Agents.Defaults != nil {
		if oc.connector.Config.Agents.Defaults.BootstrapMaxChars > 0 {
			maxChars = oc.connector.Config.Agents.Defaults.BootstrapMaxChars
		}
	}

	warn := func(message string) {
		oc.loggerForContext(ctx).Warn().Msg(message)
	}
	contextFiles := agents.BuildBootstrapContextFiles(files, maxChars, warn)
	return oc.applySoulEvilToContextFiles(ctx, store, contextFiles, maxChars)
}

func userMdHasValues(content string) bool {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return false
	}
	labels := []string{
		"name:",
		"what to call them:",
		"pronouns:",
		"timezone:",
		"notes:",
	}
	for _, rawLine := range strings.Split(trimmed, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" {
			continue
		}
		// Ignore headings.
		if strings.HasPrefix(line, "#") {
			continue
		}
		lower := strings.ToLower(line)
		for _, label := range labels {
			idx := strings.Index(lower, label)
			if idx == -1 {
				continue
			}
			after := strings.TrimSpace(line[idx+len(label):])
			after = strings.Trim(after, "*_")
			if after != "" {
				return true
			}
		}
	}
	return false
}

func (oc *AIClient) maybeAutoDeleteBootstrap(ctx context.Context, store *textfs.Store) {
	if oc == nil || store == nil {
		return
	}
	entry, found, err := store.Read(ctx, agents.DefaultBootstrapFilename)
	if err != nil || !found || entry == nil {
		return
	}

	// If the agent has filled in identity values, assume first-run is complete.
	if identityEntry, found, err := store.Read(ctx, agents.DefaultIdentityFilename); err == nil && found && identityEntry != nil {
		identity := agents.ParseIdentityMarkdown(identityEntry.Content)
		if agents.IdentityHasValues(identity) {
			if err := store.Delete(ctx, agents.DefaultBootstrapFilename); err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Msg("failed to delete BOOTSTRAP.md")
			}
			return
		}
	}

	// Fall back: if USER.md has any filled-in fields, treat bootstrap as done.
	if userEntry, found, err := store.Read(ctx, agents.DefaultUserFilename); err == nil && found && userEntry != nil {
		if userMdHasValues(userEntry.Content) {
			if err := store.Delete(ctx, agents.DefaultBootstrapFilename); err != nil {
				oc.loggerForContext(ctx).Warn().Err(err).Msg("failed to delete BOOTSTRAP.md")
			}
			return
		}
	}
}

func (oc *AIClient) applySoulEvilToContextFiles(
	ctx context.Context,
	store *textfs.Store,
	files []agents.EmbeddedContextFile,
	maxChars int,
) []agents.EmbeddedContextFile {
	if oc == nil || oc.connector == nil || oc.connector.Config.Agents == nil || oc.connector.Config.Agents.Defaults == nil {
		return files
	}
	config := oc.connector.Config.Agents.Defaults.SoulEvil
	if config == nil {
		return files
	}
	userTimezone, _ := oc.resolveUserTimezone()
	decision := agents.DecideSoulEvil(agents.SoulEvilCheckParams{
		Config:       config,
		UserTimezone: userTimezone,
		Now:          time.Now(),
	})
	if !decision.UseEvil {
		return files
	}

	entry, found, err := store.Read(ctx, decision.FileName)
	if err != nil || !found {
		oc.loggerForContext(ctx).Warn().
			Str("reason", decision.Reason).
			Str("file", decision.FileName).
			Msg("SOUL_EVIL active but file missing")
		return files
	}
	if strings.TrimSpace(entry.Content) == "" {
		oc.loggerForContext(ctx).Warn().
			Str("reason", decision.Reason).
			Str("file", decision.FileName).
			Msg("SOUL_EVIL active but file empty")
		return files
	}

	soulIndex := findSoulFileIndex(files)
	if soulIndex == -1 {
		oc.loggerForContext(ctx).Warn().
			Str("reason", decision.Reason).
			Msg("SOUL_EVIL active but SOUL.md not in bootstrap files")
		return files
	}

	trimmed := agents.TrimBootstrapContent(entry.Content, agents.DefaultSoulFilename, maxChars)
	if strings.TrimSpace(trimmed.Content) == "" {
		oc.loggerForContext(ctx).Warn().
			Str("reason", decision.Reason).
			Msg("SOUL_EVIL active but trimmed content empty")
		return files
	}

	files[soulIndex].Content = trimmed.Content
	oc.loggerForContext(ctx).Debug().
		Str("reason", decision.Reason).
		Str("file", decision.FileName).
		Msg("SOUL_EVIL active using file")
	return files
}

func findSoulFileIndex(files []agents.EmbeddedContextFile) int {
	for idx, file := range files {
		normalized := strings.ReplaceAll(strings.TrimSpace(file.Path), "\\", "/")
		base := path.Base(normalized)
		if strings.EqualFold(base, agents.DefaultSoulFilename) {
			return idx
		}
	}
	return -1
}
