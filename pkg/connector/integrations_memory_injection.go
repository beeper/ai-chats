package connector

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/ai-bridge/pkg/textfs"
)

func (oc *AIClient) injectMemoryContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	prompt []openai.ChatCompletionMessageParamUnion,
) []openai.ChatCompletionMessageParamUnion {
	// Raw/playground rooms must not inject memory context.
	if meta != nil && meta.IsRawMode {
		return prompt
	}

	if oc == nil || portal == nil || meta == nil || oc.connector == nil || oc.connector.Config.Memory == nil || !oc.connector.Config.Memory.InjectContext {
		return prompt
	}
	db := oc.bridgeDB()
	if db == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.DB == nil {
		return prompt
	}

	store := textfs.NewStore(
		db,
		string(oc.UserLogin.Bridge.DB.BridgeID),
		string(oc.UserLogin.ID),
		resolveAgentID(meta),
	)

	var sections []string
	if portal.RoomType == database.RoomTypeDM {
		if section := readMemorySection(ctx, store, "MEMORY.md"); section != "" {
			sections = append(sections, section)
		} else if section := readMemorySection(ctx, store, "memory.md"); section != "" {
			sections = append(sections, section)
		}
	}

	shouldBootstrap := meta.RecallBootstrapAt == 0
	if shouldBootstrap {
		_, loc := oc.resolveUserTimezone()
		now := time.Now().In(loc)
		today := now.Format("2006-01-02")
		yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
		if section := readMemorySection(ctx, store, fmt.Sprintf("memory/%s.md", today)); section != "" {
			sections = append(sections, section)
		}
		if section := readMemorySection(ctx, store, fmt.Sprintf("memory/%s.md", yesterday)); section != "" {
			sections = append(sections, section)
		}
		meta.RecallBootstrapAt = time.Now().UnixMilli()
		oc.savePortalQuiet(ctx, portal, "memory bootstrap")
	}

	if len(sections) == 0 {
		return prompt
	}
	contextText := strings.Join(sections, "\n\n")
	prompt = append(prompt, openai.SystemMessage(contextText))
	return prompt
}

func readMemorySection(ctx context.Context, store *textfs.Store, path string) string {
	if store == nil {
		return ""
	}
	entry, found, err := store.Read(ctx, path)
	if err != nil || !found {
		return ""
	}
	content := normalizeNewlines(entry.Content)
	trunc := textfs.TruncateHead(content, textfs.DefaultMaxLines, textfs.DefaultMaxBytes)
	if trunc.FirstLineExceedsLimit {
		return ""
	}
	text := trunc.Content
	if strings.TrimSpace(text) == "" {
		return ""
	}
	if trunc.Truncated {
		text += "\n\n[truncated]"
	}
	return fmt.Sprintf("## %s\n%s", entry.Path, text)
}
