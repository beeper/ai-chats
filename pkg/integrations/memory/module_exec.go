package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	iruntime "github.com/beeper/ai-bridge/pkg/integrations/runtime"
	"github.com/beeper/ai-bridge/pkg/shared/maputil"
	"github.com/beeper/ai-bridge/pkg/textfs"
)

const commandMaxBytes = 256 * 1024

type ToolExecDeps struct {
	GetManager             func(scope iruntime.ToolScope) (Manager, string)
	ResolveSessionKey      func(scope iruntime.ToolScope) string
	ResolveCitationsMode   func(scope iruntime.ToolScope) string
	ShouldIncludeCitations func(ctx context.Context, scope iruntime.ToolScope, mode string) bool
}

type CommandExecDeps struct {
	GetManager        func(scope iruntime.ToolScope) (Manager, string)
	ResolveSessionKey func(scope iruntime.ToolScope) string
	SplitQuotedArgs   func(raw string) ([]string, error)
	WriteFile         func(ctx context.Context, scope iruntime.CommandScope, mode string, path string, content string, maxBytes int) (updatedPath string, err error)
}

type searchOutput struct {
	Results   []SearchResult  `json:"results"`
	Provider  string          `json:"provider,omitempty"`
	Model     string          `json:"model,omitempty"`
	Fallback  *FallbackStatus `json:"fallback,omitempty"`
	Citations string          `json:"citations,omitempty"`
	Disabled  bool            `json:"disabled,omitempty"`
	Error     string          `json:"error,omitempty"`
}

type getOutput struct {
	Path     string `json:"path"`
	Text     string `json:"text"`
	Disabled bool   `json:"disabled,omitempty"`
	Error    string `json:"error,omitempty"`
}

func ExecuteTool(ctx context.Context, call iruntime.ToolCall, deps ToolExecDeps) (handled bool, result string, err error) {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	switch name {
	case "memory_search":
		text, execErr := executeSearchTool(ctx, call.Scope, call.Args, deps)
		return true, text, execErr
	case "memory_get":
		text, execErr := executeGetTool(ctx, call.Scope, call.Args, deps)
		return true, text, execErr
	default:
		return false, "", nil
	}
}

func executeSearchTool(ctx context.Context, scope iruntime.ToolScope, args map[string]any, deps ToolExecDeps) (string, error) {
	if deps.GetManager == nil {
		return "", errors.New("memory_search unavailable")
	}
	mode := strings.ToLower(maputil.StringArg(args, "mode"))
	query := maputil.StringArg(args, "query")
	if mode != "list" && query == "" {
		return "", errors.New("query required")
	}

	manager, errMsg := deps.GetManager(scope)
	if manager == nil {
		return marshalSearch(searchOutput{
			Results:  []SearchResult{},
			Disabled: true,
			Error:    errMsgOrDefault(errMsg),
		}), nil
	}

	opts := SearchOptions{
		SessionKey: resolveSessionKey(scope, deps.ResolveSessionKey),
		MinScore:   math.NaN(),
		Mode:       mode,
	}
	if max, ok := maputil.IntArg(args, "maxResults"); ok {
		opts.MaxResults = max
	}
	if score, ok := maputil.NumberArg(args, "minScore"); ok {
		opts.MinScore = score
	}
	if prefix := maputil.StringArg(args, "pathPrefix"); prefix != "" {
		opts.PathPrefix = prefix
	}
	if sources := readStringList(args, "sources"); len(sources) > 0 {
		opts.Sources = sources
	}

	searchCtx, cancel := context.WithTimeout(ctx, memorySearchTimeout)
	defer cancel()
	results, searchErr := manager.Search(searchCtx, query, opts)
	if searchErr != nil {
		return marshalSearch(searchOutput{
			Results:  []SearchResult{},
			Disabled: true,
			Error:    searchErr.Error(),
		}), nil
	}

	modeSetting := "auto"
	if deps.ResolveCitationsMode != nil {
		modeSetting = normalizeCitationsMode(deps.ResolveCitationsMode(scope))
	}
	includeCitations := true
	if deps.ShouldIncludeCitations != nil {
		includeCitations = deps.ShouldIncludeCitations(ctx, scope, modeSetting)
	}
	decorated := decorateSearchResults(results, includeCitations)
	status := manager.Status()

	return marshalSearch(searchOutput{
		Results:   decorated,
		Provider:  status.Provider,
		Model:     status.Model,
		Fallback:  status.Fallback,
		Citations: modeSetting,
	}), nil
}

func executeGetTool(ctx context.Context, scope iruntime.ToolScope, args map[string]any, deps ToolExecDeps) (string, error) {
	if deps.GetManager == nil {
		return "", errors.New("memory_get unavailable")
	}
	path := maputil.StringArg(args, "path")
	if path == "" {
		return "", errors.New("path required")
	}
	manager, errMsg := deps.GetManager(scope)
	if manager == nil {
		return marshalGet(getOutput{
			Path:     path,
			Text:     "",
			Disabled: true,
			Error:    errMsgOrDefault(errMsg),
		}), nil
	}
	var from *int
	var lines *int
	if val, ok := maputil.IntArg(args, "from"); ok {
		from = &val
	}
	if val, ok := maputil.IntArg(args, "lines"); ok {
		lines = &val
	}
	result, readErr := manager.ReadFile(ctx, path, from, lines)
	if readErr != nil {
		return marshalGet(getOutput{
			Path:     path,
			Text:     "",
			Disabled: true,
			Error:    readErr.Error(),
		}), nil
	}
	text, _ := result["text"].(string)
	resolvedPath, _ := result["path"].(string)
	if strings.TrimSpace(resolvedPath) == "" {
		resolvedPath = path
	}
	return marshalGet(getOutput{
		Path: resolvedPath,
		Text: text,
	}), nil
}

func ExecuteCommand(ctx context.Context, call iruntime.CommandCall, deps CommandExecDeps) (handled bool, err error) {
	if strings.ToLower(strings.TrimSpace(call.Name)) != "memory" {
		return false, nil
	}
	reply := call.Reply
	if reply == nil {
		reply = func(string, ...any) {}
	}
	if len(call.Args) == 0 {
		reply("Usage: !ai memory <status|reindex|search|get|set|append> ...")
		return true, nil
	}
	action := strings.ToLower(strings.TrimSpace(call.Args[0]))
	scope := iruntime.ToolScope{
		Client: call.Scope.Client,
		Portal: call.Scope.Portal,
		Meta:   call.Scope.Meta,
	}
	switch action {
	case "status":
		if deps.GetManager == nil {
			reply("Memory integration unavailable.")
			return true, nil
		}
		manager, errMsg := deps.GetManager(scope)
		if manager == nil {
			reply("Memory search disabled: %s", errMsgOrDefault(errMsg))
			return true, nil
		}
		status, statusErr := manager.StatusDetails(ctx)
		if statusErr != nil {
			reply("Couldn't load memory status: %v", statusErr)
			return true, nil
		}
		lines := formatStatusLines(status)
		reply(strings.Join(lines, "\n"))
	case "reindex":
		manager, errMsg := deps.GetManager(scope)
		if manager == nil {
			reply("Memory search disabled: %s", errMsgOrDefault(errMsg))
			return true, nil
		}
		reply("Reindexing memory...")
		onProgress := func(completed, total int, label string) {
			reply("Indexing %d/%d: %s", completed+1, total, label)
		}
		if syncErr := manager.SyncWithProgress(ctx, onProgress); syncErr != nil {
			reply("Couldn't reindex memory: %v", syncErr)
			return true, nil
		}
		reply("Memory reindex complete.")
	case "search":
		if len(call.Args) < 2 {
			reply("Usage: !ai memory search <query> [maxResults] [minScore]")
			return true, nil
		}
		manager, errMsg := deps.GetManager(scope)
		if manager == nil {
			reply("Memory search disabled: %s", errMsgOrDefault(errMsg))
			return true, nil
		}
		opts := SearchOptions{
			SessionKey: resolveSessionKey(scope, deps.ResolveSessionKey),
			MinScore:   math.NaN(),
		}
		if len(call.Args) > 2 {
			if val, convErr := strconv.Atoi(call.Args[2]); convErr == nil && val > 0 {
				opts.MaxResults = val
			}
		}
		if len(call.Args) > 3 {
			if val, convErr := strconv.ParseFloat(call.Args[3], 64); convErr == nil {
				opts.MinScore = val
			}
		}
		searchCtx, cancel := context.WithTimeout(ctx, memorySearchTimeout)
		defer cancel()
		results, searchErr := manager.Search(searchCtx, call.Args[1], opts)
		if searchErr != nil {
			reply("Couldn't search memory: %v", searchErr)
			return true, nil
		}
		if len(results) == 0 {
			reply("No matches found.")
			return true, nil
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
		replyText := trunc.Content
		if trunc.Truncated {
			replyText += "\n\n[truncated]"
		}
		reply(replyText)
	case "get":
		if len(call.Args) < 2 {
			reply("Usage: !ai memory get <path> [from] [lines]")
			return true, nil
		}
		manager, errMsg := deps.GetManager(scope)
		if manager == nil {
			reply("Memory search disabled: %s", errMsgOrDefault(errMsg))
			return true, nil
		}
		path := call.Args[1]
		var from *int
		var lines *int
		if len(call.Args) > 2 {
			if val, convErr := strconv.Atoi(call.Args[2]); convErr == nil && val > 0 {
				from = &val
			}
		}
		if len(call.Args) > 3 {
			if val, convErr := strconv.Atoi(call.Args[3]); convErr == nil && val > 0 {
				lines = &val
			}
		}
		result, readErr := manager.ReadFile(ctx, path, from, lines)
		if readErr != nil {
			reply("Couldn't read memory: %v", readErr)
			return true, nil
		}
		text, _ := result["text"].(string)
		trunc := textfs.TruncateHead(text, textfs.DefaultMaxLines, textfs.DefaultMaxBytes)
		replyText := trunc.Content
		if trunc.Truncated {
			replyText += "\n\n[truncated]"
		}
		reply(replyText)
	case "set", "append":
		if deps.SplitQuotedArgs == nil || deps.WriteFile == nil {
			reply("Memory write unavailable.")
			return true, nil
		}
		args, splitErr := deps.SplitQuotedArgs(call.RawArgs)
		if splitErr != nil {
			reply("Invalid arguments: %v", splitErr)
			return true, nil
		}
		if len(args) < 3 {
			reply("Usage: !ai memory %s <path> <content>", action)
			return true, nil
		}
		path := args[1]
		content := strings.Join(args[2:], " ")
		if len([]byte(content)) > commandMaxBytes {
			reply("Content exceeds %s limit.", textfs.FormatSize(commandMaxBytes))
			return true, nil
		}
		updatedPath, writeErr := deps.WriteFile(ctx, call.Scope, action, path, content, commandMaxBytes)
		if writeErr != nil {
			reply("Couldn't write memory: %v", writeErr)
			return true, nil
		}
		if strings.TrimSpace(updatedPath) == "" {
			updatedPath = path
		}
		reply("Memory file updated: %s", updatedPath)
	default:
		reply("Unknown memory command. Use status, reindex, get, set, or append.")
	}
	return true, nil
}

func normalizeCitationsMode(raw string) string {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "on", "off", "auto":
		return mode
	default:
		return "auto"
	}
}

func decorateSearchResults(results []SearchResult, include bool) []SearchResult {
	if !include || len(results) == 0 {
		return results
	}
	out := make([]SearchResult, 0, len(results))
	for _, entry := range results {
		next := entry
		citation := formatCitation(entry)
		if citation != "" {
			snippet := strings.TrimSpace(entry.Snippet)
			if snippet != "" {
				next.Snippet = fmt.Sprintf("%s\n\nSource: %s", snippet, citation)
			} else {
				next.Snippet = fmt.Sprintf("Source: %s", citation)
			}
		}
		out = append(out, next)
	}
	return out
}

func formatCitation(entry SearchResult) string {
	if strings.TrimSpace(entry.Path) == "" {
		return ""
	}
	if entry.StartLine > 0 && entry.EndLine > 0 {
		if entry.StartLine == entry.EndLine {
			return fmt.Sprintf("%s#L%d", entry.Path, entry.StartLine)
		}
		return fmt.Sprintf("%s#L%d-L%d", entry.Path, entry.StartLine, entry.EndLine)
	}
	return entry.Path
}

func resolveSessionKey(scope iruntime.ToolScope, fn func(scope iruntime.ToolScope) string) string {
	if fn == nil {
		return ""
	}
	return strings.TrimSpace(fn(scope))
}

func readStringList(args map[string]any, key string) []string {
	if args == nil {
		return nil
	}
	raw := args[key]
	switch list := raw.(type) {
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if s, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(s); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(list))
		for _, item := range list {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		return nil
	}
}

func marshalSearch(payload searchOutput) string {
	blob, _ := json.MarshalIndent(payload, "", "  ")
	return string(blob)
}

func marshalGet(payload getOutput) string {
	blob, _ := json.MarshalIndent(payload, "", "  ")
	return string(blob)
}

func errMsgOrDefault(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "memory integration unavailable"
	}
	return trimmed
}

func formatStatusLines(status *StatusDetails) []string {
	if status == nil {
		return []string{"Memory status unavailable."}
	}
	lines := []string{
		"Memory status:",
		fmt.Sprintf("Provider: %s", status.Provider),
		fmt.Sprintf("Model: %s", status.Model),
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
	if status.FTS != nil {
		lines = append(lines, fmt.Sprintf("FTS enabled: %t (available=%t)", status.FTS.Enabled, status.FTS.Available))
		if status.FTS.Error != "" {
			lines = append(lines, fmt.Sprintf("FTS error: %s", status.FTS.Error))
		}
	}
	if status.Cache != nil {
		lines = append(lines, fmt.Sprintf("Cache enabled: %t (entries=%d max=%d)", status.Cache.Enabled, status.Cache.Entries, status.Cache.MaxEntries))
	}
	if status.Fallback != nil {
		lines = append(lines, fmt.Sprintf("Fallback: %s (%s)", status.Fallback.From, status.Fallback.Reason))
	}
	return lines
}
