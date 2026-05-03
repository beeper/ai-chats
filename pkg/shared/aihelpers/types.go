package aihelpers

import (
	"context"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

// MessageType identifies the kind of message.
type MessageType string

const (
	MessageText  MessageType = "text"
	MessageImage MessageType = "image"
	MessageAudio MessageType = "audio"
	MessageVideo MessageType = "video"
	MessageFile  MessageType = "file"
)

// Message represents an incoming user message.
type Message struct {
	ID        string
	Text      string
	HTML      string
	MediaURL  string      // MXC URL for media messages
	MediaType string      // MIME type
	MsgType   MessageType // Text, Image, Audio, Video, File
	Sender    string
	ReplyTo   string // event ID being replied to
	Timestamp time.Time
	Metadata  map[string]any
}

// LoginInfo contains information about a bridge login.
type LoginInfo struct {
	UserID   string
	Domain   string
	Login    *bridgev2.UserLogin
	Metadata map[string]any
}

// CreateChatParams contains parameters for creating a new chat.
type CreateChatParams struct {
	UserID   string
	Name     string
	Metadata map[string]any
}

// ToolApprovalResponse is the user's decision on a tool approval request.
type ToolApprovalResponse struct {
	Approved bool
	Always   bool   // "always allow this tool"
	Reason   string // allow_once, allow_always, deny, timeout, expired
}

// ApprovalRequest describes a single approval request within a turn.
type ApprovalRequest struct {
	ApprovalID   string
	ToolCallID   string
	ToolName     string
	TTL          time.Duration
	Presentation *ApprovalPromptPresentation
	Metadata     map[string]any
}

// ApprovalHandle tracks an individual approval request.
type ApprovalHandle interface {
	ID() string
	ToolCallID() string
	Wait(ctx context.Context) (ToolApprovalResponse, error)
}

// Command defines a slash command that users can invoke.
type Command struct {
	Name        string
	Description string
	Args        string // e.g. "<query>", "[options...]"
	Handler     func(conv *Conversation, args string) error
}

// RoomFeatures describes what a room supports.
type RoomFeatures struct {
	MaxTextLength        int
	SupportsImages       bool
	SupportsAudio        bool
	SupportsVideo        bool
	SupportsFiles        bool
	SupportsReply        bool
	SupportsEdit         bool
	SupportsDelete       bool
	SupportsReactions    bool
	SupportsTyping       bool
	SupportsReadReceipts bool
	SupportsDeleteChat   bool
	CustomCapabilityID   string // for dynamic capability IDs
}

// RoomAgentSet tracks the agents available in a conversation.
type RoomAgentSet struct {
	AgentIDs []string
}

// ConversationKind identifies the runtime shape of a conversation.
type ConversationKind string

const (
	ConversationKindNormal    ConversationKind = "normal"
	ConversationKindDelegated ConversationKind = "delegated"
)

// ConversationVisibility controls whether the room should be hidden in the client.
type ConversationVisibility string

const (
	ConversationVisibilityNormal ConversationVisibility = "normal"
	ConversationVisibilityHidden ConversationVisibility = "hidden"
)

// ConversationSpec describes how to resolve or create a conversation.
type ConversationSpec struct {
	PortalID             string
	Kind                 ConversationKind
	Visibility           ConversationVisibility
	ParentConversationID string
	ParentEventID        string
	Title                string
	Metadata             map[string]any
	ArchiveOnCompletion  bool
}

// SourceKind identifies the origin of a turn.
type SourceKind string

const (
	SourceKindUserMessage SourceKind = "user_message"
	SourceKindProactive   SourceKind = "proactive"
	SourceKindSystem      SourceKind = "system"
	SourceKindBackfill    SourceKind = "backfill"
	SourceKindDelegated   SourceKind = "delegated"
	SourceKindSteering    SourceKind = "steering"
	SourceKindFollowUp    SourceKind = "follow_up"
)

// SourceRef captures the source metadata that a turn should relate to.
type SourceRef struct {
	Kind                 SourceKind
	EventID              string
	SenderID             string
	ParentConversationID string
	Metadata             map[string]any
}

// Convenience helpers for common source kinds.
func UserMessageSource(eventID string) *SourceRef {
	return &SourceRef{Kind: SourceKindUserMessage, EventID: eventID}
}

// ProviderIdentity controls provider-specific IDs and status naming used by the AI helper runtime.
type ProviderIdentity struct {
	IDPrefix      string
	LogKey        string
	StatusNetwork string
}

type SessionValue interface{}

type ConfigValue interface{}

// Config configures shared AI conversation helpers.
type Config[SessionT SessionValue, ConfigDataT ConfigValue] struct {
	Agent        *Agent
	AgentCatalog AgentCatalog

	ProviderIdentity ProviderIdentity
	TurnManagement   *TurnConfig
	RoomFeatures     *RoomFeatures
	GetCapabilities  func(session SessionT, conv *Conversation) *RoomFeatures
}
