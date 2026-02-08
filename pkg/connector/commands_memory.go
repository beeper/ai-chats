package connector

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"maunium.net/go/mautrix/bridgev2/commands"

	"github.com/beeper/ai-bridge/pkg/connector/commandregistry"
	"github.com/beeper/ai-bridge/pkg/memory"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

const memoryCommandMaxBytes = 256 * 1024

// CommandMemory handles the !ai memory command
var CommandMemory = registerAICommand(commandregistry.Definition{
	Name:           "memory",
	Description:    "Inspect and edit memory files/index",
	Args:           "<status|reindex|search|get|set|append> [...]",
	Section:        HelpSectionAI,
	RequiresPortal: true,
	RequiresLogin:  true,
	Handler:        fnMemory,
})

func fnMemory(ce *commands.Event) {
	if ce.User == nil || !ce.User.Permissions.Admin {
		ce.Reply("Only bridge admins can use this command.")
		return
	}
	client, meta, ok := requireClientMeta(ce)
	if !ok {
		return
	}
	if len(ce.Args) == 0 {
		ce.Reply("Usage: !ai memory <status|reindex|search|get|set|append> ...")
		return
	}

	switch strings.ToLower(ce.Args[0]) {
	case "status":
		manager, errMsg := getMemorySearchManager(client, resolveAgentID(meta))
		if manager == nil {
			ce.Reply("Memory search disabled: %s", errMsg)
			return
		}
		deep := false
		if len(ce.Args) > 1 {
			switch strings.ToLower(strings.TrimSpace(ce.Args[1])) {
			case "deep", "probe", "verbose":
				deep = true
			}
		}
		status, err := manager.StatusDetails(ce.Ctx)
		if err != nil {
			ce.Reply("Couldn't load memory status: %v", err)
			return
		}
		lines := []string{
			"Memory status:",
			fmt.Sprintf("Provider: %s", status.Provider),
			fmt.Sprintf("Model: %s", status.Model),
			fmt.Sprintf("Requested provider: %s", status.RequestedProvider),
			fmt.Sprintf("Workspace: %s", status.WorkspaceDir),
			fmt.Sprintf("DB: %s", status.DBPath),
			fmt.Sprintf("Sources: %s", strings.Join(status.Sources, ", ")),
			fmt.Sprintf("Extra paths: %s", strings.Join(status.ExtraPaths, ", ")),
			fmt.Sprintf("Files: %d", status.Files),
			fmt.Sprintf("Chunks: %d", status.Chunks),
		}
		if len(status.SourceCounts) > 0 {
			for _, source := range status.SourceCounts {
				lines = append(lines, fmt.Sprintf("Source %s: %d files / %d chunks", source.Source, source.Files, source.Chunks))
			}
		}
		if status.Vector != nil {
			ready := "unknown"
			if status.Vector.Available != nil {
				ready = fmt.Sprintf("%t", *status.Vector.Available)
			}
			lines = append(lines, fmt.Sprintf("Vector enabled: %t (available=%s)", status.Vector.Enabled, ready))
			if status.Vector.ExtensionPath != "" {
				lines = append(lines, fmt.Sprintf("Vector extension: %s", status.Vector.ExtensionPath))
			}
			if status.Vector.Dims > 0 {
				lines = append(lines, fmt.Sprintf("Vector dims: %d", status.Vector.Dims))
			}
			if status.Vector.LoadError != "" {
				lines = append(lines, fmt.Sprintf("Vector error: %s", status.Vector.LoadError))
			}
		}
		if status.FTS != nil {
			lines = append(lines, fmt.Sprintf("FTS enabled: %t (available=%t)", status.FTS.Enabled, status.FTS.Available))
			if status.FTS.Error != "" {
				lines = append(lines, fmt.Sprintf("FTS error: %s", status.FTS.Error))
			}
		}
		if status.Cache != nil {
			lines = append(lines, fmt.Sprintf("Cache enabled: %t (entries=%d max=%d)", status.Cache.Enabled, status.Cache.Entries, status.Cache.MaxEntries))
		}
		if status.Batch != nil {
			lines = append(lines, fmt.Sprintf("Batch enabled: %t (failures=%d limit=%d)", status.Batch.Enabled, status.Batch.Failures, status.Batch.Limit))
			lines = append(lines, fmt.Sprintf("Batch wait: %t concurrency=%d poll=%dms timeout=%dms", status.Batch.Wait, status.Batch.Concurrency, status.Batch.PollIntervalMs, status.Batch.TimeoutMs))
			if status.Batch.LastError != "" {
				lines = append(lines, fmt.Sprintf("Batch error: %s", status.Batch.LastError))
			}
			if status.Batch.LastProvider != "" {
				lines = append(lines, fmt.Sprintf("Batch provider: %s", status.Batch.LastProvider))
			}
		}
		if status.Fallback != nil {
			lines = append(lines, fmt.Sprintf("Fallback: %s (%s)", status.Fallback.From, status.Fallback.Reason))
		}
		if deep {
			if status.Vector != nil && status.Vector.Enabled {
				vectorOk := manager.ProbeVectorAvailability(ce.Ctx)
				lines = append(lines, fmt.Sprintf("Vector probe: %t", vectorOk))
			}
			embedOk, embedErr := manager.ProbeEmbeddingAvailability(ce.Ctx)
			if embedErr != "" {
				lines = append(lines, fmt.Sprintf("Embedding probe: %t (%s)", embedOk, embedErr))
			} else {
				lines = append(lines, fmt.Sprintf("Embedding probe: %t", embedOk))
			}
		}
		ce.Reply(strings.Join(lines, "\n"))
		return
	case "reindex":
		manager, errMsg := getMemorySearchManager(client, resolveAgentID(meta))
		if manager == nil {
			ce.Reply("Memory search disabled: %s", errMsg)
			return
		}
		ce.Reply("Reindexing memory...")
		onProgress := func(completed, total int, label string) {
			ce.Reply("Indexing %d/%d: %s", completed+1, total, label)
		}
		if err := manager.syncWithProgress(ce.Ctx, "", true, onProgress); err != nil {
			ce.Reply("Couldn't reindex memory: %v", err)
			return
		}
		ce.Reply("Memory reindex complete.")
		return
	case "search":
		if len(ce.Args) < 2 {
			ce.Reply("Usage: !ai memory search <query> [maxResults] [minScore]")
			return
		}
		manager, errMsg := getMemorySearchManager(client, resolveAgentID(meta))
		if manager == nil {
			ce.Reply("Memory search disabled: %s", errMsg)
			return
		}
		query := ce.Args[1]
		sessionKey := ""
		if ce.Portal != nil {
			sessionKey = ce.Portal.PortalKey.String()
		}
		opts := memory.SearchOptions{
			SessionKey: sessionKey,
			MinScore:   math.NaN(),
		}
		if len(ce.Args) > 2 {
			if val, err := strconv.Atoi(ce.Args[2]); err == nil && val > 0 {
				opts.MaxResults = val
			}
		}
		if len(ce.Args) > 3 {
			if val, err := strconv.ParseFloat(ce.Args[3], 64); err == nil {
				opts.MinScore = val
			}
		}
		results, err := manager.Search(ce.Ctx, query, opts)
		if err != nil {
			ce.Reply("Couldn't search memory: %v", err)
			return
		}
		if len(results) == 0 {
			ce.Reply("No matches found.")
			return
		}
		lines := make([]string, 0, len(results)*2)
		for _, result := range results {
			lines = append(lines, fmt.Sprintf("%.3f %s:%d-%d", result.Score, result.Path, result.StartLine, result.EndLine))
			if result.Snippet != "" {
				lines = append(lines, result.Snippet)
			}
			lines = append(lines, "")
		}
		output := strings.TrimSpace(strings.Join(lines, "\n"))
		trunc := textfs.TruncateHead(output, textfs.DefaultMaxLines, textfs.DefaultMaxBytes)
		reply := trunc.Content
		if trunc.Truncated {
			reply += "\n\n[truncated]"
		}
		ce.Reply(reply)
		return
	case "get":
		if len(ce.Args) < 2 {
			ce.Reply("Usage: !ai memory get <path> [from] [lines]")
			return
		}
		manager, errMsg := getMemorySearchManager(client, resolveAgentID(meta))
		if manager == nil {
			ce.Reply("Memory search disabled: %s", errMsg)
			return
		}
		path := ce.Args[1]
		var from *int
		var lines *int
		if len(ce.Args) > 2 {
			if val, err := strconv.Atoi(ce.Args[2]); err == nil && val > 0 {
				from = &val
			}
		}
		if len(ce.Args) > 3 {
			if val, err := strconv.Atoi(ce.Args[3]); err == nil && val > 0 {
				lines = &val
			}
		}
		result, err := manager.ReadFile(ce.Ctx, path, from, lines)
		if err != nil {
			ce.Reply("Couldn't read memory: %v", err)
			return
		}
		text, _ := result["text"].(string)
		trunc := textfs.TruncateHead(text, textfs.DefaultMaxLines, textfs.DefaultMaxBytes)
		output := trunc.Content
		if trunc.Truncated {
			output += "\n\n[truncated]"
		}
		ce.Reply(output)
		return
	case "set", "append":
		args, err := splitQuotedArgs(ce.RawArgs)
		if err != nil {
			ce.Reply("Invalid arguments: %v", err)
			return
		}
		if len(args) < 3 {
			ce.Reply("Usage: !ai memory %s <path> <content>", ce.Args[0])
			return
		}
		path := args[1]
		content := strings.Join(args[2:], " ")
		if len([]byte(content)) > memoryCommandMaxBytes {
			ce.Reply("Content exceeds %s limit.", textfs.FormatSize(memoryCommandMaxBytes))
			return
		}
		store := textfs.NewStore(
			client.UserLogin.Bridge.DB.Database,
			string(client.UserLogin.Bridge.DB.BridgeID),
			string(client.UserLogin.ID),
			resolveAgentID(meta),
		)
		if strings.ToLower(ce.Args[0]) == "append" {
			if existing, found, err := store.Read(ce.Ctx, path); err == nil && found {
				sep := "\n"
				if strings.HasSuffix(existing.Content, "\n") || existing.Content == "" {
					sep = ""
				}
				content = existing.Content + sep + content
				if len([]byte(content)) > memoryCommandMaxBytes {
					ce.Reply("Content exceeds %s limit after append.", textfs.FormatSize(memoryCommandMaxBytes))
					return
				}
			}
		}
		entry, err := store.Write(ce.Ctx, path, content)
		if err != nil {
			ce.Reply("Couldn't write memory: %v", err)
			return
		}
		if entry != nil {
			ctx := WithBridgeToolContext(ce.Ctx, &BridgeToolContext{
				Client: client,
				Portal: ce.Portal,
				Meta:   meta,
			})
			notifyMemoryFileChanged(ctx, entry.Path)
			maybeRefreshAgentIdentity(ctx, entry.Path)
		}
		ce.Reply("Memory file updated: %s", path)
		return
	default:
		ce.Reply("Unknown memory command. Use status, reindex, get, set, or append.")
		return
	}
}
