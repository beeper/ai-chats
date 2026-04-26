package agents

// ReactionGuidance controls reaction behavior in prompts.
// Matches AgentRemote's reactionGuidance with level and channel.
type ReactionGuidance struct {
	Level   string // "minimal" or "extensive"
	Channel string // e.g., "matrix", "signal"
}

// ResolvedTimeFormat mirrors AgentRemote's resolved time format type.
type ResolvedTimeFormat string

// SystemPromptParams contains all inputs for building a system prompt.
// This matches AgentRemote's buildAgentSystemPrompt params.
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
	Node         string   // Runtime version (AgentRemote uses Node)
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

// SilentReplyToken is the expected response when the agent has nothing to say.
const SilentReplyToken = "NO_REPLY"

// HeartbeatToken is the expected response for heartbeat polls.
const HeartbeatToken = "HEARTBEAT_OK"

// DefaultMaxAckChars is the max length for heartbeat acknowledgements (AgentRemote uses 300).
const DefaultMaxAckChars = 300

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
1. Ask them to start a new chat room with the "Beep" agent for that topic
2. Keep this room focused on setup and configuration

This room (Manage AI Chats) is specifically for setup and configuration. Regular conversations should happen in dedicated chat rooms with appropriate agents.

When a user asks to create or modify an agent:
1. Ask clarifying questions if needed (name, purpose, preferred model, tools)
2. Use the appropriate tool to make the changes
3. Confirm the action was successful

Remember:
- Beep is the default agent and cannot be modified or deleted
- Each agent has a unique ID, name, and configuration
- Tool profiles (minimal, coding, messaging, full) define default tool access
- Custom agents can override tool access with explicit allow/deny`
