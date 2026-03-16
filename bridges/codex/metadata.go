package codex

import (
	"slices"
	"strings"

	"go.mau.fi/util/jsontime"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"

	"github.com/beeper/agentremote"
)

type UserLoginMetadata struct {
	Provider          string   `json:"provider,omitempty"`
	CodexHome         string   `json:"codex_home,omitempty"`
	CodexAuthSource   string   `json:"codex_auth_source,omitempty"`
	CodexCommand      string   `json:"codex_command,omitempty"`
	CodexAuthMode     string   `json:"codex_auth_mode,omitempty"`
	CodexAccountEmail string   `json:"codex_account_email,omitempty"`
	ChatGPTAccountID  string   `json:"chatgpt_account_id,omitempty"`
	ChatGPTPlanType   string   `json:"chatgpt_plan_type,omitempty"`
	ChatsSynced       bool     `json:"chats_synced,omitempty"`
	ManagedPaths      []string `json:"managed_paths,omitempty"`
}

const (
	CodexAuthSourceManaged = "managed"
	CodexAuthSourceHost    = "host"
)

type PortalMetadata struct {
	Title            string `json:"title,omitempty"`
	Slug             string `json:"slug,omitempty"`
	IsCodexRoom      bool   `json:"is_codex_room,omitempty"`
	CodexThreadID    string `json:"codex_thread_id,omitempty"`
	CodexCwd         string `json:"codex_cwd,omitempty"`
	ElevatedLevel    string `json:"elevated_level,omitempty"`
	AwaitingCwdSetup bool   `json:"awaiting_cwd_setup,omitempty"`
	ManagedImport    bool   `json:"managed_import,omitempty"`
}

type MessageMetadata struct {
	agentremote.BaseMessageMetadata
	agentremote.AssistantMessageMetadata
}

type ToolCallMetadata = agentremote.ToolCallMetadata

type GhostMetadata struct {
	LastSync jsontime.Unix `json:"last_sync,omitempty"`
}

var _ database.MetaMerger = (*MessageMetadata)(nil)

func (mm *MessageMetadata) CopyFrom(other any) {
	src, ok := other.(*MessageMetadata)
	if !ok || src == nil {
		return
	}
	mm.CopyFromBase(&src.BaseMessageMetadata)
	mm.CopyFromAssistant(&src.AssistantMessageMetadata)
}

func loginMetadata(login *bridgev2.UserLogin) *UserLoginMetadata {
	return agentremote.EnsureLoginMetadata[UserLoginMetadata](login)
}

func portalMeta(portal *bridgev2.Portal) *PortalMetadata {
	return agentremote.EnsurePortalMetadata[PortalMetadata](portal)
}

func normalizedCodexAuthSource(meta *UserLoginMetadata) string {
	if meta == nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(meta.CodexAuthSource))
}

func isHostAuthLogin(meta *UserLoginMetadata) bool {
	return normalizedCodexAuthSource(meta) == CodexAuthSourceHost
}

func isManagedAuthLogin(meta *UserLoginMetadata) bool {
	return normalizedCodexAuthSource(meta) == CodexAuthSourceManaged
}

func normalizeManagedCodexPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		trimmed := strings.TrimSpace(path)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil
	}
	slices.Sort(out)
	return out
}

func managedCodexPaths(meta *UserLoginMetadata) []string {
	if meta == nil {
		return nil
	}
	meta.ManagedPaths = normalizeManagedCodexPaths(meta.ManagedPaths)
	return slices.Clone(meta.ManagedPaths)
}

func hasManagedCodexPath(meta *UserLoginMetadata, path string) bool {
	path = strings.TrimSpace(path)
	if meta == nil || path == "" {
		return false
	}
	for _, candidate := range meta.ManagedPaths {
		if strings.TrimSpace(candidate) == path {
			return true
		}
	}
	return false
}

func addManagedCodexPath(meta *UserLoginMetadata, path string) bool {
	path = strings.TrimSpace(path)
	if meta == nil || path == "" || hasManagedCodexPath(meta, path) {
		return false
	}
	meta.ManagedPaths = normalizeManagedCodexPaths(append(meta.ManagedPaths, path))
	return true
}

func removeManagedCodexPath(meta *UserLoginMetadata, path string) bool {
	path = strings.TrimSpace(path)
	if meta == nil || path == "" || len(meta.ManagedPaths) == 0 {
		return false
	}
	next := make([]string, 0, len(meta.ManagedPaths))
	removed := false
	for _, candidate := range meta.ManagedPaths {
		if strings.TrimSpace(candidate) == path {
			removed = true
			continue
		}
		next = append(next, candidate)
	}
	meta.ManagedPaths = normalizeManagedCodexPaths(next)
	return removed
}
