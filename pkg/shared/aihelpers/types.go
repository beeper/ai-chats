package aihelpers

import (
	"context"
	"sync"
	"time"

	"go.mau.fi/util/configupgrade"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
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

// Config configures the AI helper bridge.
type Config[SessionT SessionValue, ConfigDataT ConfigValue] struct {
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
	OnMessage func(session SessionT, conv *Conversation, msg *Message, turn *Turn) error

	// Session hooks (optional)
	OnConnect    func(ctx context.Context, login *LoginInfo) (SessionT, error) // returns session state
	OnDisconnect func(session SessionT)

	// Turn management (optional)
	TurnManagement *TurnConfig

	// Capabilities (optional, dynamic per-conversation)
	GetCapabilities func(session SessionT, conv *Conversation) *RoomFeatures

	// Search & chat ops (optional)
	SearchUsers       func(ctx context.Context, session SessionT, query string) ([]*bridgev2.ResolveIdentifierResponse, error)
	GetContactList    func(ctx context.Context, session SessionT) ([]*bridgev2.ResolveIdentifierResponse, error)
	ResolveIdentifier func(ctx context.Context, session SessionT, id string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error)
	CreateChat        func(ctx context.Context, session SessionT, params *CreateChatParams) (*bridgev2.CreateChatResponse, error)
	DeleteChat        func(conv *Conversation) error
	GetChatInfo       func(conv *Conversation) (*bridgev2.ChatInfo, error)
	GetUserInfo       func(ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error)
	IsThisUser        func(userID string) bool

	// Commands
	Commands []Command

	// Room features (static default; overridden by GetCapabilities if set)
	RoomFeatures *RoomFeatures // nil = AI agent defaults

	// Login — use bridgev2 types directly.
	LoginFlows    []bridgev2.LoginFlow
	GetLoginFlows func() []bridgev2.LoginFlow
	CreateLogin   func(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error)
	AcceptLogin   func(login *bridgev2.UserLogin) (bool, string)

	// Connector lifecycle and overrides.
	InitConnector       func(br *bridgev2.Bridge)
	StartConnector      func(ctx context.Context, br *bridgev2.Bridge) error
	StopConnector       func(ctx context.Context, br *bridgev2.Bridge)
	BridgeName          func() bridgev2.BridgeName
	NetworkCapabilities func() *bridgev2.NetworkGeneralCapabilities
	BridgeInfoVersion   func() (info, capabilities int)
	FillBridgeInfo      func(portal *bridgev2.Portal, content *event.BridgeEventContent)
	MakeBrokenLogin     func(login *bridgev2.UserLogin, reason string) *BrokenLoginClient
	LoadLogin           func(ctx context.Context, login *bridgev2.UserLogin) error
	CreateClient        func(login *bridgev2.UserLogin) (bridgev2.NetworkAPI, error)
	UpdateClient        func(client bridgev2.NetworkAPI, login *bridgev2.UserLogin)
	AfterLoadClient     func(client bridgev2.NetworkAPI)
	ProviderIdentity    ProviderIdentity
	ClientCacheMu       *sync.Mutex
	ClientCache         *map[networkid.UserLoginID]bridgev2.NetworkAPI

	// Backfill — use bridgev2 types directly.
	FetchMessages func(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) // nil = no backfill

	// Advanced
	ProtocolID     string // default: "ai-<Name>"
	Port           int    // default: 29400
	DBName         string // default: "<Name>.db"
	ConfigPath     string // default: auto-discover
	DBMeta         func() database.MetaTypes
	ExampleConfig  string      // YAML
	ConfigData     ConfigDataT // config struct pointer
	ConfigUpgrader configupgrade.Upgrader
}
