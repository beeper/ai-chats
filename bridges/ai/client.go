package ai

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/ptr"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/agents"
	integrationruntime "github.com/beeper/agentremote/pkg/integrations/runtime"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkAPI                       = (*AIClient)(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI    = (*AIClient)(nil)
	_ bridgev2.ContactListingNetworkAPI         = (*AIClient)(nil)
	_ bridgev2.UserSearchingNetworkAPI          = (*AIClient)(nil)
	_ bridgev2.GhostDMCreatingNetworkAPI        = (*AIClient)(nil)
	_ bridgev2.EditHandlingNetworkAPI           = (*AIClient)(nil)
	_ bridgev2.ReactionHandlingNetworkAPI       = (*AIClient)(nil)
	_ bridgev2.RedactionHandlingNetworkAPI      = (*AIClient)(nil)
	_ bridgev2.DeleteChatHandlingNetworkAPI     = (*AIClient)(nil)
	_ bridgev2.DisappearTimerChangingNetworkAPI = (*AIClient)(nil)
	_ bridgev2.TypingHandlingNetworkAPI         = (*AIClient)(nil)
	_ bridgev2.RoomNameHandlingNetworkAPI       = (*AIClient)(nil)
	_ bridgev2.RoomTopicHandlingNetworkAPI      = (*AIClient)(nil)
	_ bridgev2.RoomAvatarHandlingNetworkAPI     = (*AIClient)(nil)
)

var rejectAllMediaFileFeatures = &event.FileFeatures{
	MimeTypes: map[string]event.CapabilitySupportLevel{
		"*/*": event.CapLevelRejected,
	},
	Caption: event.CapLevelRejected,
}

func cloneRejectAllMediaFeatures() *event.FileFeatures {
	return rejectAllMediaFileFeatures.Clone()
}

// AI Chats capability constants
const (
	AIMaxTextLength        = 100000
	AIEditMaxAge           = 24 * time.Hour
	modelValidationTimeout = 5 * time.Second
)

func aiCapID() string {
	return "com.beeper.ai.capabilities.2026_02_05"
}

// aiBaseCaps defines the base capabilities for AI chat rooms
var aiBaseCaps = &event.RoomFeatures{
	ID: aiCapID(),
	Formatting: map[event.FormattingFeature]event.CapabilitySupportLevel{
		event.FmtBold:          event.CapLevelFullySupported,
		event.FmtItalic:        event.CapLevelFullySupported,
		event.FmtStrikethrough: event.CapLevelFullySupported,
		event.FmtInlineCode:    event.CapLevelFullySupported,
		event.FmtCodeBlock:     event.CapLevelFullySupported,
		event.FmtBlockquote:    event.CapLevelFullySupported,
		event.FmtUnorderedList: event.CapLevelFullySupported,
		event.FmtOrderedList:   event.CapLevelFullySupported,
		event.FmtInlineLink:    event.CapLevelFullySupported,
	},
	File: event.FileFeatureMap{
		event.MsgVideo:      cloneRejectAllMediaFeatures(),
		event.MsgAudio:      cloneRejectAllMediaFeatures(),
		event.MsgFile:       cloneRejectAllMediaFeatures(),
		event.CapMsgVoice:   cloneRejectAllMediaFeatures(),
		event.CapMsgGIF:     cloneRejectAllMediaFeatures(),
		event.CapMsgSticker: cloneRejectAllMediaFeatures(),
		event.MsgImage:      cloneRejectAllMediaFeatures(),
	},
	MaxTextLength:       AIMaxTextLength,
	LocationMessage:     event.CapLevelRejected,
	Poll:                event.CapLevelRejected,
	Reply:               event.CapLevelFullySupported,
	Thread:              event.CapLevelFullySupported,
	Edit:                event.CapLevelFullySupported,
	EditMaxCount:        10,
	EditMaxAge:          ptr.Ptr(jsontime.S(AIEditMaxAge)),
	Delete:              event.CapLevelPartialSupport,
	DeleteMaxAge:        ptr.Ptr(jsontime.S(24 * time.Hour)),
	Reaction:            event.CapLevelFullySupported,
	ReactionCount:       1,
	ReadReceipts:        true,
	TypingNotifications: true,
	Archive:             true,
	MarkAsUnread:        true,
	DeleteChat:          true,
	DisappearingTimer: &event.DisappearingTimerCapability{
		Types: []event.DisappearingType{event.DisappearingTypeAfterSend},
		Timers: []jsontime.Milliseconds{
			jsontime.MS(1 * time.Hour),
			jsontime.MS(24 * time.Hour),
			jsontime.MS(7 * 24 * time.Hour),
			jsontime.MS(90 * 24 * time.Hour),
		},
	},
}

type capabilityIDOptions struct {
	SupportsPDF        bool
	SupportsTextFiles  bool
	SupportsMsgActions bool
}

// buildCapabilityID constructs a deterministic capability ID based on model modalities
// and effective room file capabilities. Suffixes are sorted alphabetically to ensure
// the same capabilities produce the same ID.
func buildCapabilityID(caps ModelCapabilities, opts capabilityIDOptions) string {
	var suffixes []string

	// Add suffixes in alphabetical order for determinism
	if caps.SupportsAudio {
		suffixes = append(suffixes, "audio")
	}
	if caps.SupportsImageGen {
		suffixes = append(suffixes, "imagegen")
	}
	if opts.SupportsMsgActions {
		suffixes = append(suffixes, "msgactions")
	}
	if opts.SupportsPDF || caps.SupportsPDF {
		suffixes = append(suffixes, "pdf")
	}
	if opts.SupportsTextFiles {
		suffixes = append(suffixes, "textfiles")
	}
	if caps.SupportsVideo {
		suffixes = append(suffixes, "video")
	}
	if caps.SupportsVision {
		suffixes = append(suffixes, "vision")
	}

	if len(suffixes) == 0 {
		return aiCapID()
	}
	return aiCapID() + "+" + strings.Join(suffixes, "+")
}

// visionFileFeatures returns FileFeatures for vision-capable models
func visionFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"image/png":  event.CapLevelFullySupported,
			"image/jpeg": event.CapLevelFullySupported,
			"image/webp": event.CapLevelFullySupported,
			"image/gif":  event.CapLevelFullySupported,
		},
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          20 * 1024 * 1024, // 20MB
	}
}

func gifFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"image/gif": event.CapLevelFullySupported,
			"video/mp4": event.CapLevelFullySupported,
		},
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          20 * 1024 * 1024, // 20MB
	}
}

func stickerFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"image/webp": event.CapLevelFullySupported,
			"image/png":  event.CapLevelFullySupported,
			"image/gif":  event.CapLevelFullySupported,
		},
		Caption: event.CapLevelDropped,
		MaxSize: 20 * 1024 * 1024, // 20MB
	}
}

// pdfFileFeatures returns FileFeatures for PDF-capable models
func pdfFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"application/pdf": event.CapLevelFullySupported,
		},
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          50 * 1024 * 1024, // 50MB for PDFs
	}
}

func textFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes:        textFileMimeTypesMap,
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          50 * 1024 * 1024, // Shared cap with PDFs
	}
}

// audioFileFeatures returns FileFeatures for audio-capable models
func audioFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"audio/wav":              event.CapLevelFullySupported,
			"audio/x-wav":            event.CapLevelFullySupported,
			"audio/mpeg":             event.CapLevelFullySupported, // mp3
			"audio/mp3":              event.CapLevelFullySupported,
			"audio/webm":             event.CapLevelFullySupported,
			"audio/ogg":              event.CapLevelFullySupported,
			"audio/ogg; codecs=opus": event.CapLevelFullySupported,
			"audio/flac":             event.CapLevelFullySupported,
			"audio/mp4":              event.CapLevelFullySupported, // m4a
			"audio/x-m4a":            event.CapLevelFullySupported,
		},
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          25 * 1024 * 1024, // 25MB for audio
	}
}

// videoFileFeatures returns FileFeatures for video-capable models
func videoFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"video/mp4":       event.CapLevelFullySupported,
			"video/webm":      event.CapLevelFullySupported,
			"video/mpeg":      event.CapLevelFullySupported,
			"video/quicktime": event.CapLevelFullySupported, // mov
			"video/x-msvideo": event.CapLevelFullySupported, // avi
		},
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          100 * 1024 * 1024, // 100MB for video
	}
}

// AIClient handles communication with AI providers
type AIClient struct {
	sdk.ClientBase
	UserLogin *bridgev2.UserLogin
	connector *OpenAIConnector
	api       openai.Client
	apiKey    string
	log       zerolog.Logger

	// Provider abstraction layer - all providers use OpenAI SDK
	provider AIProvider

	chatLock      sync.Mutex
	bootstrapOnce sync.Once // Ensures bootstrap only runs once per client instance
	loginStateMu  sync.Mutex
	loginState    *loginRuntimeState
	loginConfigMu sync.Mutex
	loginConfig   *aiLoginConfig

	// roomLocks is the low-level occupancy guard used to serialize work per room.
	roomLocks   map[id.RoomID]bool
	roomLocksMu sync.Mutex

	// Pending message queue per room (for turn-based behavior)
	pendingQueues   map[id.RoomID]*pendingQueue
	pendingQueuesMu sync.Mutex

	// Active room runs (for interrupt/steer and tool-boundary steering).
	activeRoomRuns   map[id.RoomID]*roomRunState
	activeRoomRunsMu sync.Mutex

	// Pending group history buffers (mention-gated group context).
	groupHistoryBuffers map[id.RoomID]*groupHistoryBuffer
	groupHistoryMu      sync.Mutex

	// Subagent runs (sessions_spawn)
	subagentRuns   map[string]*subagentRun
	subagentRunsMu sync.Mutex

	// Message deduplication cache
	inboundDedupeCache *DedupeCache

	// Message debouncer for combining rapid messages
	inboundDebouncer *Debouncer

	// Matrix typing state (per room)
	userTypingMu    sync.RWMutex
	userTypingState map[id.RoomID]userTypingState

	// Typing indicator while messages are queued (per room)
	queueTypingMu sync.Mutex
	queueTyping   map[id.RoomID]*TypingController

	// Heartbeat + integrations
	scheduler          *schedulerRuntime
	integrationModules map[string]integrationruntime.ModuleHooks
	integrationOrder   []string

	toolRegistry     *toolIntegrationRegistry
	commandRegistry  *commandIntegrationRegistry
	eventRegistry    *eventIntegrationRegistry
	purgeRegistry    *purgeIntegrationRegistry
	approvalRegistry *toolApprovalIntegrationRegistry

	// Model catalog cache (VFS-backed)
	modelCatalogMu     sync.Mutex
	modelCatalogLoaded bool
	modelCatalogCache  []ModelCatalogEntry

	// MCP tool cache
	mcpToolsMu        sync.Mutex
	mcpTools          []ToolDefinition
	mcpToolSet        map[string]struct{}
	mcpToolServer     map[string]string
	mcpToolsFetchedAt time.Time

	// Tool approvals (e.g. OpenAI MCP approval requests)
	approvalFlow *sdk.ApprovalFlow[*pendingToolApprovalData]

	// Per-login cancellation: cancelled when this login disconnects.
	// All goroutines using backgroundContext() will be cancelled on disconnect.
	disconnectCtx    context.Context
	disconnectCancel context.CancelFunc
}

// pendingMessageType indicates what kind of pending message this is
type pendingMessageType string

const (
	pendingTypeText           pendingMessageType = "text"
	pendingTypeImage          pendingMessageType = "image"
	pendingTypePDF            pendingMessageType = "pdf"
	pendingTypeAudio          pendingMessageType = "audio"
	pendingTypeVideo          pendingMessageType = "video"
	pendingTypeRegenerate     pendingMessageType = "regenerate"
	pendingTypeEditRegenerate pendingMessageType = "edit_regenerate"
)

// pendingMessage represents a queued message waiting for AI processing
// Prompt is built fresh when processing starts to ensure up-to-date history
type pendingMessage struct {
	Event           *event.Event
	Portal          *bridgev2.Portal
	Meta            *PortalMetadata
	InboundContext  *airuntime.InboundContext
	Type            pendingMessageType
	MessageBody     string                   // For text, regenerate, edit_regenerate (caption for media)
	MediaURL        string                   // For media messages (image, PDF, audio, video)
	MimeType        string                   // MIME type of the media
	EncryptedFile   *event.EncryptedFileInfo // For encrypted Matrix media (E2EE rooms)
	TargetMsgID     networkid.MessageID      // For edit_regenerate
	SourceEventID   id.EventID               // For regenerate (original user message ID)
	StatusEvents    []*event.Event           // Extra events to mark sent when processing starts
	PendingSent     bool                     // Whether a pending status was already sent for this event
	RawEventContent map[string]any           // Raw Matrix event content for link previews
	AckEventIDs     []id.EventID             // Ack reactions to remove after completion
	Typing          *TypingContext
}

func newAIClient(login *bridgev2.UserLogin, connector *OpenAIConnector, apiKey string, cfg *aiLoginConfig) (*AIClient, error) {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return nil, errors.New("missing API key")
	}

	meta := login.Metadata.(*UserLoginMetadata)
	log := login.Log.With().Str("component", "ai-network").Str("provider", meta.Provider).Logger()
	log.Info().Msg("Initializing AI client")

	// Create base client struct
	oc := &AIClient{
		UserLogin:           login,
		connector:           connector,
		apiKey:              key,
		log:                 log,
		roomLocks:           make(map[id.RoomID]bool),
		pendingQueues:       make(map[id.RoomID]*pendingQueue),
		activeRoomRuns:      make(map[id.RoomID]*roomRunState),
		subagentRuns:        make(map[string]*subagentRun),
		groupHistoryBuffers: make(map[id.RoomID]*groupHistoryBuffer),
		userTypingState:     make(map[id.RoomID]userTypingState),
		queueTyping:         make(map[id.RoomID]*TypingController),
		loginConfig:         cloneAILoginConfig(cfg),
	}
	oc.InitClientBase(login, oc)
	oc.HumanUserIDPrefix = "openai-user"
	oc.MessageIDPrefix = "ai"
	oc.MessageLogKey = "ai_msg_id"
	oc.approvalFlow = sdk.NewApprovalFlow(sdk.ApprovalFlowConfig[*pendingToolApprovalData]{
		Login: func() *bridgev2.UserLogin { return oc.UserLogin },
		Sender: func(portal *bridgev2.Portal) bridgev2.EventSender {
			return oc.senderForPortal(context.Background(), portal)
		},
		BackgroundContext: oc.backgroundContext,
		RoomIDFromData: func(data *pendingToolApprovalData) id.RoomID {
			if data == nil {
				return ""
			}
			return data.RoomID
		},
		SendNotice: func(ctx context.Context, portal *bridgev2.Portal, msg string) {
			oc.sendSystemNotice(ctx, portal, msg)
		},
		IDPrefix: "ai",
		LogKey:   "ai_msg_id",
	})

	// Initialize inbound message processing with config values
	inboundCfg := connector.Config.Inbound.WithDefaults()
	oc.inboundDedupeCache = NewDedupeCache(inboundCfg.DedupeTTL, inboundCfg.DedupeMaxSize)
	debounceMs := oc.resolveInboundDebounceMs("matrix")
	log.Info().
		Dur("dedupe_ttl", inboundCfg.DedupeTTL).
		Int("dedupe_max", inboundCfg.DedupeMaxSize).
		Int("debounce_ms", debounceMs).
		Msg("Inbound processing configured")
	oc.inboundDebouncer = NewDebouncerWithLogger(debounceMs, oc.handleDebouncedMessages, func(err error, entries []DebounceEntry) {
		log.Warn().Err(err).Int("entries", len(entries)).Msg("Debounce flush failed")
	}, log)

	// Initialize provider based on login metadata.
	// All providers use the OpenAI SDK with different base URLs.
	provider, err := initProviderForLoginConfig(key, meta.Provider, cfg, connector, login, log)
	if err != nil {
		return nil, err
	}
	oc.provider = provider
	oc.api = provider.Client()

	oc.scheduler = newSchedulerRuntime(oc)
	oc.initIntegrations()

	// Load AI-local runtime state from aidb instead of bridge login metadata.
	oc.ensureLoginStateLoaded(context.Background())

	return oc, nil
}

func (oc *AIClient) SetUserLogin(login *bridgev2.UserLogin) {
	oc.UserLogin = login
	oc.ClientBase.SetUserLogin(login)
}

func (oc *AIClient) GetApprovalHandler() sdk.ApprovalReactionHandler {
	return oc.approvalFlow
}

const (
	openRouterAppReferer = "https://www.beeper.com/ai"
	openRouterAppTitle   = "Beeper"
)

func openRouterHeaders() map[string]string {
	return map[string]string{
		"HTTP-Referer": openRouterAppReferer,
		"X-Title":      openRouterAppTitle,
	}
}

func initProviderForLoginConfig(key string, providerID string, cfg *aiLoginConfig, connector *OpenAIConnector, login *bridgev2.UserLogin, log zerolog.Logger) (*OpenAIProvider, error) {
	if strings.TrimSpace(providerID) == "" {
		return nil, errors.New("login provider is required")
	}
	switch providerID {
	case ProviderOpenRouter:
		return initOpenRouterProvider(key, connector.resolveOpenRouterBaseURL(), "", connector.defaultPDFEngineForInit(), ProviderOpenRouter, log)

	case ProviderMagicProxy:
		baseURL := normalizeProxyBaseURL(loginCredentialBaseURL(cfg))
		if baseURL == "" {
			return nil, errors.New("magic proxy base_url is required")
		}
		return initOpenRouterProvider(key, joinProxyPath(baseURL, "/openrouter/v1"), "", connector.defaultPDFEngineForInit(), ProviderMagicProxy, log)

	case ProviderOpenAI:
		openaiURL := connector.resolveOpenAIBaseURL()
		log.Info().
			Str("provider", providerID).
			Str("openai_url", openaiURL).
			Msg("Initializing AI provider endpoint")
		return NewOpenAIProviderWithBaseURL(key, openaiURL, log)

	default:
		return nil, fmt.Errorf("unsupported provider: %s", providerID)
	}
}

func (oc *OpenAIConnector) defaultPDFEngineForInit() string {
	if oc != nil && oc.Config.Agents != nil && oc.Config.Agents.Defaults != nil {
		if engine := strings.TrimSpace(oc.Config.Agents.Defaults.PDFEngine); engine != "" {
			return engine
		}
	}
	return "mistral-ocr"
}

// initOpenRouterProvider creates an OpenRouter-compatible provider with PDF support.
func initOpenRouterProvider(key, url, userID, pdfEngine, providerName string, log zerolog.Logger) (*OpenAIProvider, error) {
	log.Info().
		Str("provider", providerName).
		Str("openrouter_url", url).
		Msg("Initializing AI provider endpoint")
	if pdfEngine == "" {
		pdfEngine = "mistral-ocr"
	}
	provider, err := NewOpenAIProviderWithPDFPlugin(key, url, userID, pdfEngine, openRouterHeaders(), log)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s provider: %w", providerName, err)
	}
	return provider, nil
}

// saveUserMessage persists a user message to the bridge mapping tables and
// mirrors the canonical turn into the AI-owned turn store.
func (oc *AIClient) saveUserMessage(ctx context.Context, evt *event.Event, msg *database.Message) {
	if evt != nil {
		msg.MXID = evt.ID
	}
	meta, _ := msg.Metadata.(*MessageMetadata)
	oc.loggerForContext(ctx).Debug().
		Str("message_id", string(msg.ID)).
		Str("event_id", msg.MXID.String()).
		Str("room_id", string(msg.Room.ID)).
		Str("room_receiver", string(msg.Room.Receiver)).
		Str("sender_id", string(msg.SenderID)).
		Str("meta", transcriptMetaSummary(meta)).
		Msg("Saving user message before turn persistence")
	if _, err := oc.UserLogin.Bridge.GetGhostByID(ctx, msg.SenderID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure user ghost before saving message")
	}
	portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, msg.Room)
	if err != nil || portal == nil {
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to resolve portal for AI turn persistence")
		}
		if err == nil {
			oc.loggerForContext(ctx).Debug().
				Str("message_id", string(msg.ID)).
				Str("event_id", msg.MXID.String()).
				Str("room_id", string(msg.Room.ID)).
				Str("room_receiver", string(msg.Room.Receiver)).
				Msg("Failed to resolve portal for AI turn persistence because portal lookup returned nil")
		}
		return
	}
	oc.loggerForContext(ctx).Debug().
		Str("message_id", string(msg.ID)).
		Str("event_id", msg.MXID.String()).
		Str("resolved_portal_id", string(portal.PortalKey.ID)).
		Str("resolved_portal_receiver", string(portal.PortalKey.Receiver)).
		Str("resolved_portal_mxid", portal.MXID.String()).
		Msg("Resolved portal for AI turn persistence")
	if err := oc.upsertTransportPortalMessage(ctx, portal, msg); err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to save transport user message to database")
	}
	if err := oc.persistAIConversationMessage(ctx, portal, msg); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist AI conversation turn")
	}
}

func (oc *AIClient) Connect(ctx context.Context) {
	// Create per-login cancellation context, derived from the bridge-wide background context.
	var base context.Context
	if oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.BackgroundCtx != nil {
		base = oc.UserLogin.Bridge.BackgroundCtx
	} else {
		base = context.Background()
	}
	oc.disconnectCtx, oc.disconnectCancel = context.WithCancel(base)

	oc.SetLoggedIn(false)
	oc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateConnecting,
		Message:    "Connecting",
	})

	if oc.provider != nil {
		valCtx, cancel := context.WithTimeout(oc.backgroundContext(ctx), modelValidationTimeout)
		_, err := oc.provider.ListModels(valCtx)
		cancel()
		if err != nil {
			if IsAuthError(err) {
				oc.UserLogin.BridgeState.Send(status.BridgeState{
					StateEvent: status.StateBadCredentials,
					Error:      AIAuthFailed,
					Message:    "AI login is no longer authenticated.",
				})
				return
			}
			oc.loggerForContext(ctx).Warn().Err(err).Msg("AI connect validation failed; continuing with deferred provider checks")
		}
	}

	oc.SetLoggedIn(true)
	oc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateConnected,
		Message:    "Connected",
	})

	restoreSystemEventsFromDB(oc)

	if oc.scheduler != nil {
		oc.scheduler.Start(ctx)
	}
	oc.startLifecycleIntegrations(ctx)
}

func (oc *AIClient) Disconnect() {
	// Cancel per-login context early so background goroutines stop promptly.
	if oc.disconnectCancel != nil {
		oc.disconnectCancel()
	}

	// Flush pending debounced messages before disconnect (bridgev2 pattern)
	if oc.inboundDebouncer != nil {
		oc.loggerForContext(context.Background()).Info().Msg("Flushing pending debounced messages on disconnect")
		oc.inboundDebouncer.FlushAll()
	}
	if oc.scheduler != nil {
		oc.scheduler.Stop()
	}
	oc.SetLoggedIn(false)

	oc.stopLifecycleIntegrations()
	// Stop all login-scoped integration workers for this login.
	if oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.DB != nil {
		if bridgeID, loginID := canonicalLoginBridgeID(oc.UserLogin), canonicalLoginID(oc.UserLogin); loginID != "" {
			oc.stopLoginLifecycleIntegrations(bridgeID, loginID)
		}
	}

	// Clean up per-room maps to prevent unbounded growth
	oc.roomLocksMu.Lock()
	clear(oc.roomLocks)
	oc.roomLocksMu.Unlock()

	oc.pendingQueuesMu.Lock()
	clear(oc.pendingQueues)
	oc.pendingQueuesMu.Unlock()

	if oc.approvalFlow != nil {
		oc.approvalFlow.Close()
	}

	oc.activeRoomRunsMu.Lock()
	clear(oc.activeRoomRuns)
	oc.activeRoomRunsMu.Unlock()

	oc.subagentRunsMu.Lock()
	clear(oc.subagentRuns)
	oc.subagentRunsMu.Unlock()

	oc.groupHistoryMu.Lock()
	clear(oc.groupHistoryBuffers)
	oc.groupHistoryMu.Unlock()

	oc.userTypingMu.Lock()
	clear(oc.userTypingState)
	oc.userTypingMu.Unlock()

	oc.queueTypingMu.Lock()
	for _, tc := range oc.queueTyping {
		tc.Stop()
	}
	clear(oc.queueTyping)
	oc.queueTypingMu.Unlock()

	// Report disconnected state to Matrix clients
	oc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateTransientDisconnect,
		Message:    "Disconnected",
	})
}

func (oc *AIClient) LogoutRemote(ctx context.Context) {
	if oc != nil && oc.UserLogin != nil {
		purgeLoginData(ctx, oc.UserLogin)
	}

	oc.Disconnect()

	if oc.connector != nil {
		oc.connector.clientsMu.Lock()
		delete(oc.connector.clients, oc.UserLogin.ID)
		oc.connector.clientsMu.Unlock()
	}

	oc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateLoggedOut,
		Message:    "Disconnected by user",
	})
}

func (oc *AIClient) agentUserID(agentID string) networkid.UserID {
	if oc == nil || oc.UserLogin == nil {
		return agentUserID(agentID)
	}
	return agentUserIDForLogin(oc.UserLogin.ID, agentID)
}

func (oc *AIClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	return oc.portalRoomInfo(ctx, portal), nil
}

func (oc *AIClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	ghostID := string(ghost.ID)

	// Parse agent from ghost ID (format: "agent-{id}")
	if agentID, ok := parseAgentFromGhostID(ghostID); ok {
		responder, _ := oc.ResolveResponderForGhost(ctx, ghost.ID)
		store := NewAgentStoreAdapter(oc)
		agent, agentErr := store.GetAgentByID(ctx, agentID)
		if agentErr == nil && agent != nil {
			if sdkAgent := oc.sdkAgentForDefinition(ctx, agent); sdkAgent != nil {
				info := sdkAgent.UserInfo()
				if responder != nil {
					info.ExtraProfile = responderExtraProfile(responder)
				}
				info.ExtraUpdates = updateGhostLastSync
				return info, nil
			}
		}
		if responder != nil {
			info := responderUserInfo(responder, agentContactIdentifiers(agentID), true)
			info.ExtraUpdates = updateGhostLastSync
			return info, nil
		}
		return &bridgev2.UserInfo{
			Name:         ptr.Ptr("Unknown Agent"),
			IsBot:        ptr.Ptr(true),
			Identifiers:  stringutil.DedupeStrings([]string{canonicalAgentIdentifier(agentID)}),
			ExtraUpdates: updateGhostLastSync,
		}, nil
	}

	// Parse model from ghost ID (format: "model-{escaped-model-id}")
	if modelID := parseModelFromGhostID(ghostID); modelID != "" {
		if responder, err := oc.ResolveResponderForGhost(ctx, ghost.ID); err == nil && responder != nil {
			userInfo := responderUserInfo(responder, modelContactIdentifiers(modelID), false)
			userInfo.ExtraUpdates = updateGhostLastSync
			return userInfo, nil
		}
		return &bridgev2.UserInfo{
			Name:         ptr.Ptr(modelContactName(modelID, oc.findModelInfo(modelID))),
			IsBot:        ptr.Ptr(false),
			Identifiers:  modelContactIdentifiers(modelID),
			ExtraUpdates: updateGhostLastSync,
		}, nil
	}

	// Fallback for unknown ghost types
	return &bridgev2.UserInfo{
		Name:  ptr.Ptr("AI Assistant"),
		IsBot: ptr.Ptr(false),
	}, nil
}

// updateGhostLastSync updates the ghost's LastSync timestamp
func updateGhostLastSync(_ context.Context, ghost *bridgev2.Ghost) bool {
	meta, ok := ghost.Metadata.(*GhostMetadata)
	if !ok || meta == nil {
		ghost.Metadata = &GhostMetadata{LastSync: jsontime.U(time.Now())}
		return true
	}
	// Force save if last sync was more than 24 hours ago
	forceSave := time.Since(meta.LastSync.Time) > 24*time.Hour
	meta.LastSync = jsontime.U(time.Now())
	return forceSave
}

func (oc *AIClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	meta := portalMeta(portal)
	isModelRoom := meta != nil && meta.ResolvedTarget != nil && meta.ResolvedTarget.Kind == ResolvedTargetModel

	// Always recompute effective room capabilities from the resolved room target.
	modelCaps := oc.getRoomCapabilities(ctx, meta)
	allowTextFiles := oc.canUseMediaUnderstanding(meta)
	supportsPDF := modelCaps.SupportsPDF || oc.isOpenRouterProvider()
	supportsMsgActions := oc.supportsMessageActionsFeature(meta)

	// Clone base capabilities
	caps := aiBaseCaps.Clone()

	// Build dynamic capability ID from modalities
	caps.ID = buildCapabilityID(modelCaps, capabilityIDOptions{
		SupportsPDF:        supportsPDF,
		SupportsTextFiles:  allowTextFiles,
		SupportsMsgActions: supportsMsgActions,
	})

	if supportsMsgActions {
		caps.Reply = event.CapLevelFullySupported
		caps.Reaction = event.CapLevelFullySupported
		caps.ReactionCount = 1
		if isModelRoom {
			caps.Reply = event.CapLevelRejected
			caps.Thread = event.CapLevelRejected
			caps.Edit = event.CapLevelRejected
			caps.EditMaxCount = 0
			caps.EditMaxAge = nil
		} else {
			caps.Edit = event.CapLevelFullySupported
			caps.EditMaxCount = 10
			caps.EditMaxAge = ptr.Ptr(jsontime.S(AIEditMaxAge))
		}
	} else {
		// Use explicit rejected levels so features remain visible in
		// com.beeper.room_features instead of being omitted by omitempty.
		caps.Reply = event.CapLevelRejected
		caps.Edit = event.CapLevelRejected
		caps.EditMaxCount = 0
		caps.EditMaxAge = nil
		caps.Reaction = event.CapLevelRejected
		caps.ReactionCount = 0
	}

	if isModelRoom {
		caps.Reply = event.CapLevelRejected
		caps.Thread = event.CapLevelRejected
	}

	// Apply file capabilities based on modalities
	if modelCaps.SupportsVision {
		caps.File[event.MsgImage] = visionFileFeatures()
		caps.File[event.CapMsgGIF] = gifFileFeatures()
		caps.File[event.CapMsgSticker] = stickerFileFeatures()
	}

	fileFeatures := cloneRejectAllMediaFeatures()
	fileEnabled := false

	// OpenRouter/Beeper: all models support PDF via file-parser plugin
	// For other providers, check model's native PDF support
	if supportsPDF {
		for mime := range pdfFileFeatures().MimeTypes {
			fileFeatures.MimeTypes[mime] = event.CapLevelFullySupported
		}
		fileEnabled = true
	}
	if allowTextFiles {
		for mime := range textFileFeatures().MimeTypes {
			fileFeatures.MimeTypes[mime] = event.CapLevelFullySupported
		}
		fileEnabled = true
	}
	if fileEnabled {
		fileFeatures.Caption = event.CapLevelFullySupported
		fileFeatures.MaxCaptionLength = AIMaxTextLength
		fileFeatures.MaxSize = 50 * 1024 * 1024
		caps.File[event.MsgFile] = fileFeatures
	}

	if modelCaps.SupportsAudio {
		caps.File[event.MsgAudio] = audioFileFeatures()
		// Allow voice notes when audio understanding is available.
		caps.File[event.CapMsgVoice] = audioFileFeatures()
	}
	if modelCaps.SupportsVideo {
		caps.File[event.MsgVideo] = videoFileFeatures()
	}
	// Note: ImageGen is output capability - doesn't affect file upload features
	// Note: Reasoning is processing mode - doesn't affect room features

	return caps
}

func (oc *AIClient) supportsMessageActionsFeature(meta *PortalMetadata) bool {
	if meta == nil {
		return false
	}
	if oc == nil {
		return true
	}
	if oc.connector == nil {
		return true
	}
	return oc.isToolEnabled(meta, ToolNameMessage)
}

// effectiveModel returns the full prefixed model ID (e.g., "openai/gpt-5.2")
// based only on the resolved room target.
func (oc *AIClient) effectiveModel(meta *PortalMetadata) string {
	responder := oc.responderForMeta(context.Background(), meta)
	if responder == nil {
		return ""
	}
	return responder.ModelID
}

// effectiveModelForAPI returns the actual model name to send to the API
// For OpenRouter/Beeper, returns the full model ID (e.g., "openai/gpt-5.2")
// For direct providers, strips the prefix (e.g., "openai/gpt-5.2" → "gpt-5.2")
func (oc *AIClient) effectiveModelForAPI(meta *PortalMetadata) string {
	modelID := oc.effectiveModel(meta)

	// OpenRouter and Beeper route through a gateway that expects the full model ID
	if oc.isOpenRouterProvider() {
		return modelID
	}

	// Direct OpenAI provider needs the prefix stripped
	_, actualModel := ParseModelPrefix(modelID)
	return actualModel
}

// modelIDForAPI converts a full model ID to the provider-specific API model name.
// For OpenRouter-compatible providers, returns the full model ID.
// For direct providers, strips the prefix (e.g., "openai/gpt-5.2" → "gpt-5.2").
func (oc *AIClient) modelIDForAPI(modelID string) string {
	if oc.isOpenRouterProvider() {
		return modelID
	}
	_, actualModel := ParseModelPrefix(modelID)
	return actualModel
}

// defaultModelForProvider returns the configured default model for this login's provider
func (oc *AIClient) defaultModelForProvider() string {
	if oc == nil || oc.connector == nil || oc.UserLogin == nil {
		return DefaultModelOpenRouter
	}
	switch loginMetadata(oc.UserLogin).Provider {
	case ProviderOpenAI:
		return oc.defaultModelSelection(ProviderOpenAI).Primary
	case ProviderOpenRouter, ProviderMagicProxy:
		return oc.defaultModelSelection(ProviderOpenRouter).Primary
	default:
		return DefaultModelOpenRouter
	}
}

func (oc *AIClient) defaultModelSelection(provider string) ModelSelectionConfig {
	if oc == nil || oc.connector == nil || oc.connector.Config.Agents == nil || oc.connector.Config.Agents.Defaults == nil || oc.connector.Config.Agents.Defaults.Model == nil {
		return ModelSelectionConfig{Primary: defaultModelForProviderName(provider)}
	}
	selection := *oc.connector.Config.Agents.Defaults.Model
	if strings.TrimSpace(selection.Primary) == "" {
		selection.Primary = defaultModelForProviderName(provider)
	}
	return selection
}

func defaultModelForProviderName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case ProviderOpenAI:
		return DefaultModelOpenAI
	case ProviderOpenRouter, ProviderMagicProxy:
		return DefaultModelOpenRouter
	default:
		return DefaultModelOpenRouter
	}
}

// effectivePrompt returns the base system prompt to use for non-agent rooms.
func (oc *AIClient) effectivePrompt(meta *PortalMetadata) string {
	base := oc.connector.Config.DefaultSystemPrompt
	supplement := oc.profilePromptSupplement()
	if supplement == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return supplement
	}
	return fmt.Sprintf("%s\n\n%s", base, supplement)
}

func (oc *AIClient) profilePromptSupplement() string {
	if oc == nil || oc.UserLogin == nil {
		return strings.TrimSpace(oc.gravatarContext())
	}
	loginCfg := oc.loginConfigSnapshot(context.Background())

	var lines []string
	if profile := loginCfg.Profile; profile != nil {
		if v := strings.TrimSpace(profile.Name); v != "" {
			lines = append(lines, "Name: "+v)
		}
		if v := strings.TrimSpace(profile.Occupation); v != "" {
			lines = append(lines, "Occupation: "+v)
		}
		if v := strings.TrimSpace(profile.AboutUser); v != "" {
			lines = append(lines, "About the user: "+v)
		}
		if v := strings.TrimSpace(profile.CustomInstructions); v != "" {
			lines = append(lines, "Custom instructions: "+v)
		}
	}
	if gravatar := strings.TrimSpace(oc.gravatarContext()); gravatar != "" {
		lines = append(lines, gravatar)
	}
	if len(lines) == 0 {
		return ""
	}
	return "User profile:\n- " + strings.Join(lines, "\n- ")
}

// getLinkPreviewConfig returns the link preview configuration, with defaults filled in.
func getLinkPreviewConfig(connectorConfig *Config) LinkPreviewConfig {
	config := DefaultLinkPreviewConfig()

	if connectorConfig.Tools.Links != nil {
		cfg := connectorConfig.Tools.Links
		// Apply explicit settings only if they differ from zero values
		if !cfg.Enabled {
			config.Enabled = cfg.Enabled
		}
		if cfg.MaxURLsInbound > 0 {
			config.MaxURLsInbound = cfg.MaxURLsInbound
		}
		if cfg.MaxURLsOutbound > 0 {
			config.MaxURLsOutbound = cfg.MaxURLsOutbound
		}
		if cfg.FetchTimeout > 0 {
			config.FetchTimeout = cfg.FetchTimeout
		}
		if cfg.MaxContentChars > 0 {
			config.MaxContentChars = cfg.MaxContentChars
		}
		if cfg.MaxPageBytes > 0 {
			config.MaxPageBytes = cfg.MaxPageBytes
		}
		if cfg.MaxImageBytes > 0 {
			config.MaxImageBytes = cfg.MaxImageBytes
		}
		if cfg.CacheTTL > 0 {
			config.CacheTTL = cfg.CacheTTL
		}
	}

	return config
}

// effectiveAgentPrompt returns the resolved agent prompt for the current room target.
func (oc *AIClient) effectiveAgentPrompt(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) string {
	if meta == nil {
		return ""
	}

	agentID := resolveAgentID(meta)
	if agentID == "" {
		return ""
	}

	// Load the agent
	store := NewAgentStoreAdapter(oc)
	agent, err := store.GetAgentByID(ctx, agentID)
	if err != nil || agent == nil {
		oc.loggerForContext(ctx).Warn().Err(err).Str("agent", agentID).Msg("Failed to load agent for prompt")
		return ""
	}

	timezone, _ := oc.resolveUserTimezone()

	workspaceDir := resolvePromptWorkspaceDir()
	var extraParts []string
	if strings.TrimSpace(agent.SystemPrompt) != "" {
		extraParts = append(extraParts, strings.TrimSpace(agent.SystemPrompt))
	}
	extraSystemPrompt := strings.Join(extraParts, "\n\n")

	// Build params for prompt generation (OpenClaw template)
	params := agents.SystemPromptParams{
		WorkspaceDir:      workspaceDir,
		ExtraSystemPrompt: extraSystemPrompt,
		UserTimezone:      timezone,
		PromptMode:        agent.PromptMode,
		HeartbeatPrompt:   resolveHeartbeatPrompt(&oc.connector.Config, resolveHeartbeatConfig(&oc.connector.Config, agent.ID), agent),
	}
	if oc.connector != nil && oc.connector.Config.Modules != nil {
		if memCfg, ok := oc.connector.Config.Modules["memory"].(map[string]any); ok {
			if citations, ok := memCfg["citations"].(string); ok {
				params.MemoryCitations = strings.TrimSpace(citations)
			}
		}
	}
	params.UserIdentitySupplement = oc.profilePromptSupplement()
	params.ContextFiles = oc.buildBootstrapContextFiles(ctx, agentID, meta)
	if meta != nil && strings.TrimSpace(meta.SubagentParentRoomID) != "" {
		params.PromptMode = agents.PromptModeMinimal
	}

	availableTools := oc.buildAvailableTools(meta)
	if len(availableTools) > 0 {
		toolNames := make([]string, 0, len(availableTools))
		toolSummaries := make(map[string]string)
		for _, tool := range availableTools {
			if !tool.Enabled {
				continue
			}
			toolNames = append(toolNames, tool.Name)
			if strings.TrimSpace(tool.Description) != "" {
				toolSummaries[strings.ToLower(tool.Name)] = tool.Description
			}
		}
		params.ToolNames = toolNames
		params.ToolSummaries = toolSummaries
	}

	modelCaps := oc.getModelCapabilitiesForMeta(ctx, meta)

	// Build capabilities list from model resolution
	var caps []string
	if modelCaps.SupportsVision {
		caps = append(caps, "vision")
	}
	if modelCaps.SupportsToolCalling {
		caps = append(caps, "tools")
	}
	if modelCaps.SupportsReasoning {
		caps = append(caps, "reasoning")
	}
	if modelCaps.SupportsAudio {
		caps = append(caps, "audio")
	}
	if modelCaps.SupportsVideo {
		caps = append(caps, "video")
	}

	host, _ := os.Hostname()
	params.RuntimeInfo = &agents.RuntimeInfo{
		AgentID:      agent.ID,
		Host:         host,
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Node:         runtime.Version(),
		Model:        oc.effectiveModel(meta),
		DefaultModel: oc.defaultModelForProvider(),
		Channel:      "matrix",
		Capabilities: caps,
		RepoRoot:     "",
	}

	// Reaction guidance - default to minimal for group chats
	if portal != nil && oc.isGroupChat(ctx, portal) {
		params.ReactionGuidance = &agents.ReactionGuidance{
			Level:   "minimal",
			Channel: "matrix",
		}
	}

	// Reasoning hints and level
	params.ReasoningTagHint = false
	params.ReasoningLevel = resolvePromptReasoningLevel(meta)

	// Default thinking level (OpenClaw-style): low for reasoning-capable models, otherwise off.
	params.DefaultThinkLevel = oc.defaultThinkLevel(meta)

	return agents.BuildSystemPrompt(params)
}

func (oc *AIClient) effectiveTemperature(meta *PortalMetadata) *float64 {
	if meta != nil && meta.ResolvedTarget != nil && meta.ResolvedTarget.Kind == ResolvedTargetAgent {
		store := NewAgentStoreAdapter(oc)
		agent, err := store.GetAgentByID(context.Background(), meta.ResolvedTarget.AgentID)
		if err == nil && agent != nil {
			return ptr.Clone(agent.Temperature)
		}
	}
	return nil
}

// defaultThinkLevel resolves the default think level in an OpenClaw-compatible way:
// low for reasoning-capable models, off otherwise.
func (oc *AIClient) defaultThinkLevel(meta *PortalMetadata) string {
	switch effort := strings.ToLower(strings.TrimSpace(oc.effectiveReasoningEffort(meta))); effort {
	case "off", "none":
		return "off"
	case "minimal":
		return "low"
	case "low", "medium", "high", "xhigh":
		return effort
	}
	if caps := oc.getModelCapabilitiesForMeta(context.Background(), meta); caps.SupportsReasoning {
		return "low"
	}
	if modelID := strings.TrimSpace(oc.effectiveModel(meta)); modelID != "" {
		if info := oc.findModelInfo(modelID); info != nil && info.SupportsReasoning {
			return "low"
		}
	}
	return "off"
}

func (oc *AIClient) effectiveReasoningEffort(meta *PortalMetadata) string {
	if !oc.getModelCapabilitiesForMeta(context.Background(), meta).SupportsReasoning {
		return ""
	}
	if meta != nil {
		switch effort := strings.ToLower(strings.TrimSpace(meta.RuntimeReasoning)); effort {
		case "low", "medium", "high":
			return effort
		case "off", "none":
			return ""
		}
	}
	return defaultReasoningEffort
}

func (oc *AIClient) historyLimit(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata) int {
	isGroup := portal != nil && oc.isGroupChat(ctx, portal)
	if oc != nil && oc.connector != nil && oc.connector.Config.Messages != nil {
		if isGroup {
			if cfg := oc.connector.Config.Messages.GroupChat; cfg != nil && cfg.HistoryLimit >= 0 {
				return cfg.HistoryLimit
			}
			return defaultGroupContextMessages
		}
		if cfg := oc.connector.Config.Messages.DirectChat; cfg != nil && cfg.HistoryLimit >= 0 {
			return cfg.HistoryLimit
		}
	}
	if isGroup {
		return defaultGroupContextMessages
	}
	return defaultMaxContextMessages
}

func (oc *AIClient) effectiveMaxTokens(meta *PortalMetadata) int {
	var maxTokens int
	modelID := oc.effectiveModel(meta)
	if info := oc.findModelInfo(modelID); info != nil && info.MaxOutputTokens > 0 {
		maxTokens = info.MaxOutputTokens
	} else {
		maxTokens = defaultMaxTokens
	}
	// Cap at context window to prevent impossible requests.
	// When max output tokens >= context window (common for thinking/reasoning
	// models where thinking tokens count toward output), we must leave headroom
	// for the input prompt, otherwise the API rejects the request immediately.
	if cw := oc.getModelContextWindow(meta); cw > 0 && maxTokens >= cw {
		maxTokens = cw * 3 / 4 // leave 25% of context window for input
	}
	return maxTokens
}

// isOpenRouterProvider checks if the current provider uses the OpenRouter-compatible API surface.
func (oc *AIClient) isOpenRouterProvider() bool {
	provider := loginMetadata(oc.UserLogin).Provider
	return provider == ProviderOpenRouter || provider == ProviderMagicProxy
}

// isGroupChat determines if the portal is a group chat.
// Prefer explicit portal metadata over member count to avoid misclassifying DMs
// that include extra ghosts (e.g. AI model users).
func (oc *AIClient) isGroupChat(ctx context.Context, portal *bridgev2.Portal) bool {
	if portal == nil || portal.MXID == "" {
		return false
	}

	switch portal.RoomType {
	case database.RoomTypeDM:
		return false
	case database.RoomTypeGroupDM, database.RoomTypeSpace:
		return true
	}
	if portal.OtherUserID != "" {
		return false
	}

	// Fallback to member count when portal type is unknown.
	matrixConn := oc.UserLogin.Bridge.Matrix
	if matrixConn == nil {
		return false
	}
	members, err := matrixConn.GetMembers(ctx, portal.MXID)
	if err != nil {
		oc.loggerForContext(ctx).Debug().Err(err).Msg("Failed to get joined members for group chat detection")
		return false
	}

	// Group chat = more than 2 members (user + bot = 1:1, user + bot + others = group)
	return len(members) > 2
}

func (oc *AIClient) defaultPDFEngine() string {
	if oc != nil && oc.connector != nil {
		return oc.connector.defaultPDFEngineForInit()
	}
	return "mistral-ocr"
}

// effectivePDFEngine returns the PDF engine to use for the given portal.
// Priority: room-level PDFConfig > agent defaults > default "mistral-ocr"
func (oc *AIClient) effectivePDFEngine(meta *PortalMetadata) string {
	// Room-level override
	if meta != nil && meta.PDFConfig != nil && meta.PDFConfig.Engine != "" {
		return meta.PDFConfig.Engine
	}
	return oc.defaultPDFEngine()
}

// validateModel checks if a model is available for this user
func (oc *AIClient) validateModel(ctx context.Context, modelID string) (bool, error) {
	if modelID == "" {
		return true, nil
	}
	_, found, err := oc.resolveModelID(ctx, modelID)
	return found, err
}

func candidateModelLookupIDs(modelID string) []string {
	normalized := strings.TrimSpace(modelID)
	if normalized == "" {
		return nil
	}
	candidates := []string{normalized}
	decoded, err := url.PathUnescape(normalized)
	if err == nil {
		decoded = strings.TrimSpace(decoded)
		if decoded != "" && decoded != normalized {
			candidates = append(candidates, decoded)
		}
	}
	return candidates
}

// resolveModelID validates canonical model IDs only (hard-cut mode).
func (oc *AIClient) resolveModelID(ctx context.Context, modelID string) (string, bool, error) {
	candidates := candidateModelLookupIDs(modelID)
	if len(candidates) == 0 {
		return "", true, nil
	}

	models, err := oc.listAvailableModels(ctx, false)
	if err == nil && len(models) > 0 {
		for _, candidate := range candidates {
			for _, model := range models {
				if model.ID == candidate {
					return model.ID, true, nil
				}
			}
		}
	}

	for _, candidate := range candidates {
		if fallback := resolveModelIDFromManifest(candidate); fallback != "" {
			return fallback, true, nil
		}
	}

	return "", false, nil
}

func resolveModelIDFromManifest(modelID string) string {
	normalized := strings.TrimSpace(modelID)
	if normalized == "" {
		return ""
	}

	if _, ok := ModelManifest.Models[normalized]; ok {
		return normalized
	}
	return ""
}

// listAvailableModels loads models from the derived catalog and caches them.
// The implicit catalog is fed from the OpenRouter-backed manifest.
func (oc *AIClient) listAvailableModels(ctx context.Context, forceRefresh bool) ([]ModelInfo, error) {
	state := oc.loginStateSnapshot(ctx)

	// Check cache (refresh every 6 hours unless forced)
	if !forceRefresh && state.ModelCache != nil {
		age := time.Now().Unix() - state.ModelCache.LastRefresh
		if age < state.ModelCache.CacheDuration {
			return state.ModelCache.Models, nil
		}
	}

	oc.loggerForContext(ctx).Debug().Msg("Loading derived model catalog")
	allModels := oc.loadModelCatalogModels(ctx)

	if err := oc.updateLoginState(ctx, func(state *loginRuntimeState) bool {
		if state.ModelCache == nil {
			state.ModelCache = &ModelCache{
				CacheDuration: int64(oc.connector.Config.ModelCacheDuration.Seconds()),
			}
		}
		state.ModelCache.Models = allModels
		state.ModelCache.LastRefresh = time.Now().Unix()
		if state.ModelCache.CacheDuration == 0 {
			state.ModelCache.CacheDuration = int64(oc.connector.Config.ModelCacheDuration.Seconds())
		}
		return true
	}); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to save model cache")
	}

	oc.loggerForContext(ctx).Info().Int("count", len(allModels)).Msg("Cached available models")
	return allModels, nil
}

// findModelInfo looks up ModelInfo from the user's model cache by ID
func (oc *AIClient) findModelInfo(modelID string) *ModelInfo {
	state := oc.loginStateSnapshot(context.Background())
	if state != nil && state.ModelCache != nil {
		for i := range state.ModelCache.Models {
			if state.ModelCache.Models[i].ID == modelID {
				return &state.ModelCache.Models[i]
			}
		}
	}
	return oc.findModelInfoInCatalog(modelID)
}

// maxHistoryImageMessages limits how many recent history messages can have images injected,
// to keep token usage under control.
const maxHistoryImageMessages = 10

// updateAssistantGeneratedFiles finds the most recent assistant message with tool calls
// in the portal and appends the given GeneratedFileRef entries to its metadata.
// This is used by async image generation to link generated images back to the assistant
// turn that triggered them, so the model can reference them via [media_url: ...] in history.
func (oc *AIClient) updateAssistantGeneratedFiles(ctx context.Context, portal *bridgev2.Portal, refs []GeneratedFileRef) {
	if len(refs) == 0 {
		return
	}
	messages, err := oc.getAIHistoryMessages(ctx, portal, 10)
	if err != nil {
		oc.Log().Warn().Err(err).Msg("Failed to load messages for async GeneratedFiles update")
		return
	}
	for _, msg := range messages {
		meta, ok := msg.Metadata.(*MessageMetadata)
		if !ok || meta.Role != "assistant" || !meta.HasToolCalls {
			continue
		}
		// Found the most recent assistant message with tool calls; update the canonical conversation turn.
		transcriptMsg, stateErr := oc.loadAIConversationMessage(ctx, portal, msg.ID, msg.MXID)
		if stateErr != nil {
			oc.Log().Warn().Err(stateErr).Str("msg_id", string(msg.ID)).Msg("Failed to load assistant conversation turn")
			return
		}
		if transcriptMsg == nil {
			transcriptMsg = cloneMessageForAIHistory(msg)
		}
		transcriptMeta, ok := transcriptMsg.Metadata.(*MessageMetadata)
		if !ok || transcriptMeta == nil {
			transcriptMeta = cloneMessageMetadata(meta)
			transcriptMsg.Metadata = transcriptMeta
		}
		transcriptMeta.GeneratedFiles = append(append([]GeneratedFileRef(nil), transcriptMeta.GeneratedFiles...), refs...)
		if err := oc.persistAIConversationMessage(ctx, portal, transcriptMsg); err != nil {
			oc.Log().Warn().Err(err).Str("msg_id", string(msg.ID)).Msg("Failed to persist assistant conversation GeneratedFiles")
		} else {
			oc.Log().Debug().Str("msg_id", string(msg.ID)).Int("files", len(refs)).Msg("Updated assistant conversation GeneratedFiles")
		}
		return
	}
	oc.Log().Warn().Msg("No assistant message found to update with async GeneratedFiles")
}

type historyLoadResult struct {
	rows      []*database.Message
	hasVision bool
	limit     int
}

func (oc *AIClient) loadHistoryMessages(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) ([]PromptMessage, error) {
	return oc.replayHistoryMessages(ctx, portal, meta, historyReplayOptions{mode: historyReplayNormal})
}

func (oc *AIClient) buildBaseContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) (PromptContext, error) {
	promptContext := PromptContext{
		SystemPrompt: oc.buildConversationSystemPromptText(ctx, portal, meta, true),
	}

	historyMessages, err := oc.loadHistoryMessages(ctx, portal, meta)
	if err != nil {
		return PromptContext{}, err
	}
	promptContext.Messages = append(promptContext.Messages, historyMessages...)

	return promptContext, nil
}

func (oc *AIClient) applyAbortHint(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, body string) string {
	if meta == nil || !meta.AbortedLastRun {
		return body
	}
	meta.AbortedLastRun = false
	if portal != nil {
		oc.savePortalQuiet(ctx, portal, "abort hint")
	}
	note := "Note: The previous agent run was aborted by the user. Resume carefully or ask for clarification."
	if strings.TrimSpace(body) == "" {
		return note
	}
	return note + "\n\n" + body
}

type inboundPromptResult struct {
	PromptContext   PromptContext
	ResolvedBody    string // user message after body override + abort hint
	UntrustedPrefix string // context prefix to prepend to the resolved user body
}

// prepareInboundPromptContext builds the base context, resolves inbound context,
// appends trusted inbound metadata to the system prompt, resolves body overrides,
// and applies the abort hint. Untrusted inbound prefixes are returned separately
// so callers can place them deterministically in the user prompt body.
func (oc *AIClient) prepareInboundPromptContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	userText string,
	eventID id.EventID,
) (inboundPromptResult, error) {
	promptContext := PromptContext{
		SystemPrompt: oc.buildConversationSystemPromptText(ctx, portal, meta, true),
	}
	historyMessages, err := oc.replayHistoryMessages(ctx, portal, meta, historyReplayOptions{
		mode:             historyReplayNormal,
		excludeMessageID: sdk.MatrixMessageID(eventID),
	})
	if err != nil {
		return inboundPromptResult{}, err
	}
	promptContext.Messages = append(promptContext.Messages, historyMessages...)
	inboundCtx := oc.resolvePromptInboundContext(ctx, portal, userText, eventID)
	AppendPromptText(&promptContext.SystemPrompt, airuntime.BuildInboundMetaSystemPrompt(inboundCtx))

	resolved := strings.TrimSpace(userText)
	if body := strings.TrimSpace(inboundCtx.BodyForAgent); body != "" {
		resolved = body
	}

	resolved = oc.applyAbortHint(ctx, portal, meta, resolved)
	untrustedPrefix := strings.TrimSpace(airuntime.BuildInboundUserContextPrefix(inboundCtx))

	return inboundPromptResult{
		PromptContext:   promptContext,
		ResolvedBody:    resolved,
		UntrustedPrefix: untrustedPrefix,
	}, nil
}

// buildLinkContext extracts URLs from the message, fetches previews, and returns formatted context.
func (oc *AIClient) buildLinkContext(ctx context.Context, message string, rawEventContent map[string]any) string {
	config := getLinkPreviewConfig(&oc.connector.Config)
	if !config.Enabled {
		return ""
	}

	// Extract URLs from message
	urls := ExtractURLs(message, config.MaxURLsInbound)
	if len(urls) == 0 {
		return ""
	}

	// Check for existing previews in the event
	var existingPreviews []*event.BeeperLinkPreview
	if rawEventContent != nil {
		existingPreviews = ParseExistingLinkPreviews(rawEventContent)
	}

	// Build map of existing previews by URL
	existingByURL := make(map[string]*event.BeeperLinkPreview)
	for _, p := range existingPreviews {
		if p.MatchedURL != "" {
			existingByURL[p.MatchedURL] = p
		}
		if p.CanonicalURL != "" {
			existingByURL[p.CanonicalURL] = p
		}
	}

	// Find URLs that need fetching
	var urlsToFetch []string
	var allPreviews []*event.BeeperLinkPreview
	for _, u := range urls {
		if existing, ok := existingByURL[u]; ok {
			allPreviews = append(allPreviews, existing)
		} else {
			urlsToFetch = append(urlsToFetch, u)
		}
	}

	// Fetch missing previews
	if len(urlsToFetch) > 0 {
		previewer := NewLinkPreviewer(config)
		fetchCtx, cancel := context.WithTimeout(ctx, config.FetchTimeout*time.Duration(len(urlsToFetch)))
		defer cancel()

		// For inbound context, we don't need to upload images - just extract the text data
		fetchedWithImages := previewer.FetchPreviews(fetchCtx, urlsToFetch)
		fetched := ExtractBeeperPreviews(fetchedWithImages)
		allPreviews = append(allPreviews, fetched...)
	}

	if len(allPreviews) == 0 {
		return ""
	}

	return FormatPreviewsForContext(allPreviews, config.MaxContentChars)
}

// buildMediaTurnContext builds a prompt turn with media content.
func (oc *AIClient) buildMediaTurnContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	caption string,
	mediaURL string,
	mimeType string,
	encryptedFile *event.EncryptedFileInfo,
	mediaType pendingMessageType,
	eventID id.EventID,
) (PromptContext, error) {
	return oc.buildPromptContextForTurn(ctx, portal, meta, caption, eventID, currentTurnPromptOptions{
		currentTurnTextOptions: currentTurnTextOptions{includeLinkScope: true},
		attachment: &turnAttachmentOptions{
			mediaURL:      mediaURL,
			mimeType:      mimeType,
			encryptedFile: encryptedFile,
			mediaType:     mediaType,
		},
	})
}

// buildPromptUpToMessage builds a prompt including messages up to and including the specified message
func (oc *AIClient) buildContextUpToMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	targetMessageID networkid.MessageID,
	newBody string,
) (PromptContext, error) {
	base := PromptContext{
		SystemPrompt: oc.buildConversationSystemPromptText(ctx, portal, meta, false),
	}
	historyMessages, err := oc.replayHistoryMessages(ctx, portal, meta, historyReplayOptions{
		mode:            historyReplayRewrite,
		targetMessageID: targetMessageID,
	})
	if err != nil {
		return PromptContext{}, err
	}
	base.Messages = append(base.Messages, historyMessages...)
	body := strings.TrimSpace(newBody)
	body = airuntime.SanitizeChatMessageForDisplay(body, true)
	base.Messages = append(base.Messages, newUserTextPromptMessage(body))
	return base, nil
}

// downloadAndEncodeMedia downloads media and returns base64-encoded data.
// maxSizeMB limits the download size (0 = no limit).
func (oc *AIClient) downloadAndEncodeMedia(ctx context.Context, mxcURL string, encryptedFile *event.EncryptedFileInfo, maxSizeMB int) (string, string, error) {
	maxBytes := 0
	if maxSizeMB > 0 {
		maxBytes = maxSizeMB * 1024 * 1024
	}
	data, mimeType, err := oc.downloadMediaBytes(ctx, mxcURL, encryptedFile, maxBytes, "")
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(data), mimeType, nil
}

// ensureGhostDisplayName ensures the ghost has its display name set before sending messages.
// This fixes the issue where ghosts appear with raw user IDs instead of formatted names.
func (oc *AIClient) ensureGhostDisplayName(ctx context.Context, modelID string) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return
	}
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, modelUserID(modelID))
	if err != nil || ghost == nil {
		return
	}
	oc.ensureGhostDisplayNameWithGhost(ctx, ghost, modelID, oc.findModelInfo(modelID))
}

func (oc *AIClient) ensureGhostDisplayNameWithGhost(ctx context.Context, ghost *bridgev2.Ghost, modelID string, info *ModelInfo) {
	if ghost == nil {
		return
	}
	displayName := modelContactName(modelID, info)
	if ghost.Name == "" || !ghost.NameSet || ghost.Name != displayName {
		ghost.UpdateInfo(ctx, &bridgev2.UserInfo{
			Name:        ptr.Ptr(displayName),
			IsBot:       ptr.Ptr(false),
			Identifiers: modelContactIdentifiers(modelID),
		})
		oc.loggerForContext(ctx).Debug().Str("model", modelID).Str("name", displayName).Msg("Updated ghost display name")
	}
}

// ensureAgentGhostDisplayName ensures the agent ghost has its display name set.
func (oc *AIClient) ensureAgentGhostDisplayName(ctx context.Context, agentID, modelID, agentName string) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return
	}
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, oc.agentUserID(agentID))
	if err != nil || ghost == nil {
		return
	}
	displayName := agentName
	var avatar *bridgev2.Avatar
	if agentID != "" {
		store := NewAgentStoreAdapter(oc)
		if agent, err := store.GetAgentByID(ctx, agentID); err == nil && agent != nil {
			avatarURL := strings.TrimSpace(agent.AvatarURL)
			if avatarURL != "" {
				avatar = &bridgev2.Avatar{
					ID:  networkid.AvatarID(avatarURL),
					MXC: id.ContentURIString(avatarURL),
				}
			}
		}
	}
	shouldUpdate := ghost.Name == "" || !ghost.NameSet || ghost.Name != displayName
	if avatar != nil {
		if !ghost.AvatarSet || ghost.AvatarMXC != avatar.MXC || ghost.AvatarID != avatar.ID {
			shouldUpdate = true
		}
	} else if ghost.AvatarMXC != "" && ghost.AvatarSet {
		avatar = &bridgev2.Avatar{Remove: true}
		shouldUpdate = true
	}
	if shouldUpdate {
		ghost.UpdateInfo(ctx, &bridgev2.UserInfo{
			Name:        ptr.Ptr(displayName),
			IsBot:       ptr.Ptr(true),
			Identifiers: agentContactIdentifiers(agentID),
			Avatar:      avatar,
		})
		oc.loggerForContext(ctx).Debug().Str("agent", agentID).Str("model", modelID).Str("name", displayName).Msg("Updated agent ghost display name")
	}
}

// ensureModelInRoom ensures the current portal sender ghost is joined to the portal room.
// The sender may be a model ghost or an agent ghost depending on the portal target.
func (oc *AIClient) ensureModelInRoom(ctx context.Context, portal *bridgev2.Portal) error {
	if portal == nil || portal.MXID == "" {
		return errors.New("invalid portal")
	}
	_, _, err := oc.resolvePortalSenderAndIntent(ctx, portal, bridgev2.RemoteEventMessage, true)
	return err
}

func (oc *AIClient) loggerForContext(ctx context.Context) *zerolog.Logger {
	return sdk.LoggerFromContext(ctx, &oc.log)
}

func (oc *AIClient) backgroundContext(ctx context.Context) context.Context {
	var base context.Context
	// Use the per-login disconnectCtx so goroutines are cancelled on disconnect.
	if oc.disconnectCtx != nil {
		base = oc.disconnectCtx
	} else if oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.BackgroundCtx != nil {
		base = oc.UserLogin.Bridge.BackgroundCtx
	} else {
		base = context.Background()
	}

	if model, ok := modelOverrideFromContext(ctx); ok {
		base = withModelOverride(base, model)
	}
	return oc.loggerForContext(ctx).WithContext(base)
}

// buildDedupeKey creates a unique key for inbound message deduplication.
// Format: matrix|{loginID}|{roomID}|{eventID}
func (oc *AIClient) buildDedupeKey(roomID id.RoomID, eventID id.EventID) string {
	return fmt.Sprintf("matrix|%s|%s|%s", oc.UserLogin.ID, roomID, eventID)
}

// handleDebouncedMessages processes flushed debounce buffer entries.
// This combines multiple rapid messages into a single AI request.
func (oc *AIClient) handleDebouncedMessages(entries []DebounceEntry) {
	if len(entries) == 0 {
		return
	}

	ctx := oc.backgroundContext(context.Background())
	last := entries[len(entries)-1]
	if last.Meta != nil {
		if override := oc.effectiveModel(last.Meta); strings.TrimSpace(override) != "" {
			ctx = withModelOverride(ctx, override)
		}
	}

	// Combine raw bodies if multiple
	combinedRaw, _ := CombineDebounceEntries(entries)

	combinedBody := oc.buildMatrixInboundBody(ctx, last.Portal, last.Meta, last.Event, combinedRaw, last.SenderName, last.RoomName, last.IsGroup)
	inboundCtx := oc.buildMatrixInboundContext(last.Portal, last.Event, combinedRaw, last.SenderName, last.RoomName, last.IsGroup)
	ctx = withInboundContext(ctx, inboundCtx)
	rawEventContent := map[string]any(nil)
	if last.Event != nil && last.Event.Content.Raw != nil {
		rawEventContent = clonePendingRawMap(last.Event.Content.Raw)
	}
	pendingEvent := snapshotPendingEvent(last.Event)

	extraStatusEvents := make([]*event.Event, 0, len(entries)-1)
	if len(entries) > 1 {
		for _, entry := range entries[:len(entries)-1] {
			if entry.Event != nil {
				extraStatusEvents = append(extraStatusEvents, entry.Event)
			}
		}
	}
	statusCtx := ctx
	if len(extraStatusEvents) > 0 {
		statusCtx = context.WithValue(ctx, statusEventsKey{}, extraStatusEvents)
	}

	// Build prompt with combined body
	promptContext, err := oc.buildCurrentTurnWithLinks(statusCtx, last.Portal, last.Meta, combinedBody, rawEventContent, last.Event.ID)
	if err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to build prompt for debounced messages")
		oc.notifyMatrixSendFailure(statusCtx, last.Portal, last.Event, err)
		if last.Meta.AckReactionRemoveAfter && entries[0].AckEventID != "" {
			oc.removeAckReactionByID(statusCtx, last.Portal, entries[0].AckEventID)
		}
		return
	}
	// Create user message for database
	userMessage := &database.Message{
		ID:       sdk.MatrixMessageID(last.Event.ID),
		MXID:     last.Event.ID,
		Room:     last.Portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: sdk.BaseMessageMetadata{Role: "user", Body: combinedBody},
		},
		Timestamp: sdk.MatrixEventTimestamp(last.Event),
	}
	setCanonicalTurnDataFromPromptMessages(userMessage.Metadata.(*MessageMetadata), promptTail(promptContext, 1))

	// Save user message to database - we must do this ourselves since we already
	// returned Pending: true to the bridge framework when debouncing started
	// Ensure ghost row exists to avoid foreign key violations.
	if _, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userMessage.SenderID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure user ghost before saving debounced message")
	}
	oc.saveUserMessage(ctx, last.Event, userMessage)

	// Dispatch using existing flow (handles room lock + status)
	// Pass nil for userMessage since we already saved it above
	ackRemoveIDs := make([]id.EventID, 0, len(entries))
	for _, entry := range entries {
		if entry.Event != nil {
			ackRemoveIDs = append(ackRemoveIDs, entry.Event.ID)
		}
	}

	pending := pendingMessage{
		Event:           pendingEvent,
		Portal:          last.Portal,
		Meta:            last.Meta,
		InboundContext:  &inboundCtx,
		Type:            pendingTypeText,
		MessageBody:     combinedBody,
		StatusEvents:    extraStatusEvents,
		PendingSent:     last.PendingSent,
		RawEventContent: rawEventContent,
		AckEventIDs:     ackRemoveIDs,
		Typing: &TypingContext{
			IsGroup:      last.IsGroup,
			WasMentioned: last.WasMentioned,
		},
	}
	queueItem := pendingQueueItem{
		pending:         pending,
		messageID:       string(pendingEvent.ID),
		summaryLine:     combinedRaw,
		enqueuedAt:      time.Now().UnixMilli(),
		rawEventContent: rawEventContent,
	}
	queueSettings := oc.resolveQueueSettingsForPortal(statusCtx, last.Portal, last.Meta, "", airuntime.QueueInlineOptions{})

	_, _ = oc.dispatchOrQueue(statusCtx, pendingEvent, last.Portal, last.Meta, nil, queueItem, queueSettings, promptContext)

}

// removeAckReactionByID removes an ack reaction by its event ID.
func (oc *AIClient) removeAckReactionByID(ctx context.Context, portal *bridgev2.Portal, reactionEventID id.EventID) {
	if portal == nil || portal.MXID == "" || reactionEventID == "" {
		return
	}

	if err := oc.redactEventViaPortal(ctx, portal, reactionEventID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).
			Stringer("reaction_event", reactionEventID).
			Msg("Failed to remove ack reaction by ID")
	} else {
		oc.loggerForContext(ctx).Debug().
			Stringer("reaction_event", reactionEventID).
			Msg("Removed ack reaction by ID")
	}
}
