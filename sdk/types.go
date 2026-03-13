package sdk

import (
	"context"
	"time"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
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

	// Escape hatches for power users.
	RawEvent *event.Event
	RawMsg   *bridgev2.MatrixMessage
}

// MessageEdit represents an edit to a previously sent message.
type MessageEdit struct {
	OriginalID string
	NewText    string
	NewHTML    string
	RawEdit    *bridgev2.MatrixEdit
}

// Reaction represents a user reaction on a message.
type Reaction struct {
	MessageID string
	Emoji     string
	Sender    string
	RawMsg    *bridgev2.MatrixReaction
}

// LoginInfo contains information about a bridge login.
type LoginInfo struct {
	UserID   string
	Domain   string
	Login    *bridgev2.UserLogin // escape hatch
	Metadata map[string]any
}

// UserInfo describes a user/agent/model for search results.
type UserInfo struct {
	ID       string
	Name     string
	Avatar   string
	Metadata map[string]any
}

// ChatInfo describes a chat/portal.
type ChatInfo struct {
	ID       string
	Name     string
	Topic    string
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
	ToolCallID   string
	ToolName     string
	TTL          time.Duration
	Blocking     bool
	Presentation *agentremote.ApprovalPromptPresentation
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
	CustomCapabilityID   string              // for dynamic capability IDs
	Custom               *event.RoomFeatures // escape hatch: override everything
}

// RoomAgentSet tracks the agents available in a conversation.
type RoomAgentSet struct {
	PrimaryAgentID string
	AgentIDs       []string
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
	PrimaryAgentID       string
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
	ParentConversationID string
	Metadata             map[string]any
}

// Convenience helpers for common source kinds.
func UserMessageSource(eventID string) *SourceRef {
	return &SourceRef{Kind: SourceKindUserMessage, EventID: eventID}
}

func ProactiveSource() *SourceRef {
	return &SourceRef{Kind: SourceKindProactive}
}

func SystemSource(eventID string) *SourceRef {
	return &SourceRef{Kind: SourceKindSystem, EventID: eventID}
}

func BackfillSource(eventID string) *SourceRef {
	return &SourceRef{Kind: SourceKindBackfill, EventID: eventID}
}

func DelegatedSource(parentConversationID, eventID string) *SourceRef {
	return &SourceRef{
		Kind:                 SourceKindDelegated,
		EventID:              eventID,
		ParentConversationID: parentConversationID,
	}
}

// ModelInfo describes an AI model.
type ModelInfo struct {
	ID           string
	Name         string
	Provider     string
	Capabilities []string
}

// Config configures the SDK bridge.
type Config struct {
	// Required
	Name        string
	Description string

	// Agent identity (optional, used for ghost sender)
	Agent *Agent
	// Optional agent catalog used for contact listing and room agent management.
	AgentCatalog AgentCatalog

	// Message handling (required)
	// session is the value returned by OnConnect; conv is the conversation;
	// msg is the incoming message; turn is the pre-created Turn for streaming responses.
	OnMessage func(session any, conv *Conversation, msg *Message, turn *Turn) error

	// Event hooks (optional)
	OnConnect    func(ctx context.Context, login *LoginInfo) (any, error) // returns session state
	OnDisconnect func(session any)
	OnReaction   func(session any, conv *Conversation, reaction *Reaction) error
	OnTyping     func(session any, conv *Conversation, typing bool)
	OnEdit       func(session any, conv *Conversation, edit *MessageEdit) error
	OnDelete     func(session any, conv *Conversation, msgID string) error
	OnRoomName   func(session any, conv *Conversation, name string) (bool, error)
	OnRoomTopic  func(session any, conv *Conversation, topic string) (bool, error)

	// Turn management (optional)
	TurnManagement *TurnConfig

	// Capabilities (optional, dynamic per-conversation)
	GetCapabilities func(session any, conv *Conversation) *RoomFeatures

	// Search & chat ops (optional)
	SearchUsers       func(query string) ([]*UserInfo, error)
	GetContactList    func() ([]*UserInfo, error)
	ResolveIdentifier func(id string) (*UserInfo, error)
	CreateChat        func(params *CreateChatParams) (*ChatInfo, error)
	DeleteChat        func(conv *Conversation) error
	GetChatInfo       func(conv *Conversation) (*bridgev2.ChatInfo, error)
	GetUserInfo       func(ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error)
	IsThisUser        func(userID string) bool

	// Commands
	Commands []Command

	// Room features (static default; overridden by GetCapabilities if set)
	RoomFeatures *RoomFeatures // nil = AI agent defaults

	// Login — use bridgev2 types directly.
	LoginFlows  []bridgev2.LoginFlow                                                                         // nil = single auto-login
	CreateLogin func(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) // nil = auto-login

	// Backfill — use bridgev2 types directly.
	FetchMessages func(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) // nil = no backfill

	// Import turns for backfill (optional, session-aware)
	ImportTurns func(session any, conv *Conversation, params BackfillParams) ([]*ImportedTurn, error)

	// Advanced
	ProtocolID     string                    // default: "sdk-<Name>"
	Port           int                       // default: 29400
	DBName         string                    // default: "<Name>.db"
	ConfigPath     string                    // default: auto-discover
	DBMeta         func() database.MetaTypes // nil = default
	ExampleConfig  string                    // YAML
	ConfigData     any                       // config struct pointer
	ConfigUpgrader configupgrade.Upgrader
}
