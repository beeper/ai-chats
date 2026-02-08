package agents

import (
	"regexp"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
)

var (
	silentReplyPrefixRE    = regexp.MustCompile(`(?i)^\s*` + regexp.QuoteMeta(SilentReplyToken) + `(?:$|\W)`)
	silentReplySuffixRE    = regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(SilentReplyToken) + `\W*$`)
	heartbeatReplyPrefixRE = regexp.MustCompile(`(?i)^\s*` + regexp.QuoteMeta(HeartbeatToken) + `(?:$|\W)`)
	heartbeatReplySuffixRE = regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(HeartbeatToken) + `\W*$`)
)

// ReactionGuidance controls reaction behavior in prompts.
// Matches OpenClaw's reactionGuidance with level and channel.
type ReactionGuidance struct {
	Level   string // "minimal" or "extensive"
	Channel string // e.g., "matrix", "signal"
}

// ResolvedTimeFormat mirrors OpenClaw's resolved time format type.
type ResolvedTimeFormat string

// SystemPromptParams contains all inputs for building a system prompt.
// This matches OpenClaw's buildAgentSystemPrompt params.
type SystemPromptParams struct {
	WorkspaceDir           string
	DefaultThinkLevel      string
	ReasoningLevel         string
	ExtraSystemPrompt      string
	OwnerNumbers           []string
	ReasoningTagHint       bool
	ToolNames              []string
	ToolSummaries          map[string]string
	ModelAliasLines        []string
	UserTimezone           string
	UserTime               string
	UserTimeFormat         ResolvedTimeFormat
	ContextFiles           []EmbeddedContextFile
	SkillsPrompt           string
	HeartbeatPrompt        string
	DocsPath               string
	WorkspaceNotes         []string
	TTSHint                string
	PromptMode             PromptMode
	RuntimeInfo            *RuntimeInfo
	MessageToolHints       []string
	SandboxInfo            *SandboxInfo
	ReactionGuidance       *ReactionGuidance
	MemoryCitations        string
	UserIdentitySupplement string
}

// RuntimeInfo contains runtime context for the LLM.
type RuntimeInfo struct {
	AgentID      string   // Current agent ID
	Host         string   // Hostname
	OS           string   // Host OS
	Arch         string   // Host architecture
	Node         string   // Runtime version (OpenClaw uses Node)
	Model        string   // Current model being used
	DefaultModel string   // Default model for the provider
	Channel      string   // Communication channel
	Capabilities []string // Runtime capabilities
	RepoRoot     string   // Repo root path
}

// EmbeddedContextFile represents an injected project context file.
type EmbeddedContextFile struct {
	Path    string
	Content string
}

// SandboxInfo describes the current sandbox environment.
type SandboxInfo struct {
	Enabled             bool
	WorkspaceDir        string
	WorkspaceAccess     string // "none", "ro", "rw"
	AgentWorkspaceMount string
	BrowserBridgeURL    string
	BrowserNoVncURL     string
	HostBrowserAllowed  *bool
	Elevated            *ElevatedInfo
}

// ElevatedInfo describes elevated tool availability.
type ElevatedInfo struct {
	Allowed      bool
	DefaultLevel string // "on" | "off" | "ask" | "full"
}

// SubagentPromptParams contains inputs for building a subagent system prompt.
// Matches OpenClaw's buildSubagentSystemPrompt from subagent-announce.ts.
type SubagentPromptParams struct {
	RequesterSessionKey string // Session key of the agent that spawned this subagent
	RequesterChannel    string // Channel the requester is on (e.g., "matrix", "signal")
	ChildSessionKey     string // Session key of this subagent
	Label               string // Optional label for the task
	Task                string // Description of the task to complete
}

// SilentReplyToken is the expected response when the agent has nothing to say.
const SilentReplyToken = "NO_REPLY"

// HeartbeatToken is the expected response for heartbeat polls.
const HeartbeatToken = "HEARTBEAT_OK"

// DefaultMaxAckChars is the max length for heartbeat acknowledgements (OpenClaw uses 300).
const DefaultMaxAckChars = 300

// IsSilentReplyText checks if the given text is a silent reply token.
// Handles edge cases like markdown wrapping: **NO_REPLY**, `NO_REPLY`, etc.
// Matches OpenClaw's isSilentReplyText from tokens.ts.
func IsSilentReplyText(text string) bool {
	return containsToken(text, SilentReplyToken)
}

// IsHeartbeatReplyText checks if the given text is a heartbeat reply token.
// Handles edge cases like markdown wrapping: **HEARTBEAT_OK**, etc.
func IsHeartbeatReplyText(text string) bool {
	return containsToken(text, HeartbeatToken)
}

// containsToken checks if text contains token at start/end, handling markdown/HTML wrapping.
// Based on OpenClaw's isSilentReplyText pattern.
func containsToken(text, token string) bool {
	if text == "" {
		return false
	}
	stripped := stringutil.StripMarkup(text)
	trimmed := strings.TrimSpace(stripped)

	if trimmed == token {
		return true
	}

	var prefixRE, suffixRE *regexp.Regexp
	switch token {
	case SilentReplyToken:
		prefixRE = silentReplyPrefixRE
		suffixRE = silentReplySuffixRE
	case HeartbeatToken:
		prefixRE = heartbeatReplyPrefixRE
		suffixRE = heartbeatReplySuffixRE
	default:
		escaped := regexp.QuoteMeta(token)
		prefixRE = regexp.MustCompile(`(?i)^\s*` + escaped + `(?:$|\W)`)
		suffixRE = regexp.MustCompile(`(?i)\b` + escaped + `\W*$`)
	}
	if prefixRE.MatchString(stripped) {
		return true
	}
	return suffixRE.MatchString(stripped)
}

// StripHeartbeatToken removes the heartbeat token from text and returns the remaining content.
// This preserves legacy behavior for non-heartbeat messages.
func StripHeartbeatToken(text string, maxAckChars int) (shouldSkip bool, strippedText string, didStrip bool) {
	return StripHeartbeatTokenWithMode(text, StripHeartbeatModeMessage, maxAckChars)
}

// DefaultSystemPrompt is the default prompt for general-purpose agents.
const DefaultSystemPrompt = `You are a personal assistant called Beep. You run inside the Beeper app.`

// BossSystemPrompt is the system prompt for the Boss agent.
const BossSystemPrompt = `You are the Agent Builder, an AI that helps users manage their AI chats and create custom AI agents.

This room is called "Manage AI Chats" - it's where users come to configure their AI experience.

Your capabilities:
1. Create and manage chat rooms
2. Create new agents with custom personalities, system prompts, and tool configurations
3. Fork existing agents to create modified copies
4. Edit custom agents (but not preset agents)
5. Delete custom agents
6. List all available agents
7. List available models and tools

IMPORTANT - Handling non-setup conversations:
If a user wants to chat about anything OTHER than agent/room management (e.g., asking questions, having a conversation, getting help with tasks), you should:
1. Ask them to start a new chat room with the "beeper" agent for that topic
2. Keep this room focused on setup and configuration

This room (Manage AI Chats) is specifically for setup and configuration. Regular conversations should happen in dedicated chat rooms with appropriate agents.

When a user asks to create or modify an agent:
1. Ask clarifying questions if needed (name, purpose, preferred model, tools)
2. Use the appropriate tool to make the changes
3. Confirm the action was successful

Remember:
- Beep is the default agent and cannot be modified or deleted
- Each agent has a unique ID, name, and configuration
- Tool profiles (minimal, coding, full) define default tool access
- Custom agents can override tool access with explicit allow/deny`
