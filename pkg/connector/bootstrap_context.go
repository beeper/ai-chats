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
	db := oc.bridgeDB()
	if db == nil {
		return nil
	}
	if strings.TrimSpace(agentID) == "" {
		agentID = "default"
	}
	store := textfs.NewStore(
		db,
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
	// USER.md includes placeholder hints like "*(optional)*" which should not count as "filled in".
	// Normalize extracted values and ignore known placeholders.
	normalizeValue := func(value string) string {
		v := strings.TrimSpace(value)
		// Strip common markdown emphasis markers. Do this a couple times because removing "**"
		// can expose leading whitespace before another marker like "*...*".
		for i := 0; i < 2; i++ {
			v = strings.TrimSpace(strings.Trim(v, "*_"))
		}
		if strings.HasPrefix(v, "(") && strings.HasSuffix(v, ")") {
			v = strings.TrimSpace(v[1 : len(v)-1])
		}
		// Match identity normalization: normalize fancy dashes and collapse whitespace.
		replacer := strings.NewReplacer("\u2013", "-", "\u2014", "-")
		v = replacer.Replace(v)
		v = strings.Join(strings.Fields(v), " ")
		v = strings.ToLower(v)
		return v
	}
	isPlaceholder := func(value string) bool {
		switch normalizeValue(value) {
		case "", "optional":
			return true
		default:
			return false
		}
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
		// Only treat the "profile fields" list items as authoritative; other sections may mention labels.
		if !(strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*")) {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "-*"))

		// Parse "- **Label:** value" style lines. Avoid substring matching to prevent bold markers
		// (e.g. "**Pronouns:**") from polluting the extracted value.
		colonIndex := strings.Index(line, ":")
		if colonIndex == -1 {
			continue
		}
		label := strings.ToLower(strings.TrimSpace(strings.Trim(line[:colonIndex], "*_")))
		value := strings.TrimSpace(line[colonIndex+1:])
		if isPlaceholder(value) {
			continue
		}
		switch label {
		case "name", "what to call them", "pronouns", "timezone", "notes":
			return true
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
