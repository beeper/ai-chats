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

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/agents"
	airuntime "github.com/beeper/agentremote/pkg/runtime"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	bridgesdk "github.com/beeper/agentremote/sdk"
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
	_ bridgev2.DisappearTimerChangingNetworkAPI = (*AIClient)(nil)
	_ bridgev2.TypingHandlingNetworkAPI         = (*AIClient)(nil)
	_ bridgev2.ReadReceiptHandlingNetworkAPI    = (*AIClient)(nil)
	_ bridgev2.RoomNameHandlingNetworkAPI       = (*AIClient)(nil)
	_ bridgev2.RoomTopicHandlingNetworkAPI      = (*AIClient)(nil)
	_ bridgev2.RoomAvatarHandlingNetworkAPI     = (*AIClient)(nil)
	_ bridgev2.MuteHandlingNetworkAPI           = (*AIClient)(nil)
	_ bridgev2.MarkedUnreadHandlingNetworkAPI   = (*AIClient)(nil)
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
	agentremote.ClientBase
	UserLogin *bridgev2.UserLogin
	connector *OpenAIConnector
	api       openai.Client
	apiKey    string
	log       zerolog.Logger

	// Provider abstraction layer - all providers use OpenAI SDK
	provider AIProvider

	chatLock      sync.Mutex
	bootstrapOnce sync.Once // Ensures bootstrap only runs once per client instance

	// Turn-based message queuing: only one response per room at a time
	activeRooms   map[id.RoomID]bool
	activeRoomsMu sync.Mutex

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
	integrationModules map[string]any
	integrationOrder   []string

	toolRegistry     *toolIntegrationRegistry
	promptRegistry   *promptIntegrationRegistry
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
	approvalFlow *agentremote.ApprovalFlow[*pendingToolApprovalData]

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

func newAIClient(login *bridgev2.UserLogin, connector *OpenAIConnector, apiKey string) (*AIClient, error) {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return nil, errors.New("missing API key")
	}

	// Get per-user credentials from login metadata
	meta := login.Metadata.(*UserLoginMetadata)
	log := login.Log.With().Str("component", "ai-network").Str("provider", meta.Provider).Logger()
	log.Info().Msg("Initializing AI client")

	// Create base client struct
	oc := &AIClient{
		UserLogin:           login,
		connector:           connector,
		apiKey:              key,
		log:                 log,
		activeRooms:         make(map[id.RoomID]bool),
		pendingQueues:       make(map[id.RoomID]*pendingQueue),
		activeRoomRuns:      make(map[id.RoomID]*roomRunState),
		subagentRuns:        make(map[string]*subagentRun),
		groupHistoryBuffers: make(map[id.RoomID]*groupHistoryBuffer),
		userTypingState:     make(map[id.RoomID]userTypingState),
		queueTyping:         make(map[id.RoomID]*TypingController),
	}
	oc.InitClientBase(login, oc)
	oc.HumanUserIDPrefix = "openai-user"
	oc.MessageIDPrefix = "ai"
	oc.MessageLogKey = "ai_msg_id"
	oc.approvalFlow = agentremote.NewApprovalFlow(agentremote.ApprovalFlowConfig[*pendingToolApprovalData]{
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
	provider, err := initProviderForLogin(key, meta, connector, login, log)
	if err != nil {
		return nil, err
	}
	oc.provider = provider
	oc.api = provider.Client()

	oc.scheduler = newSchedulerRuntime(oc)
	oc.initIntegrations()

	// Seed last-heartbeat snapshot from persisted login metadata (command-only surface).
	if meta != nil && meta.LastHeartbeatEvent != nil {
		seedLastHeartbeatEvent(login.ID, meta.LastHeartbeatEvent)
	}

	return oc, nil
}

func (oc *AIClient) SetUserLogin(login *bridgev2.UserLogin) {
	oc.UserLogin = login
	oc.ClientBase.SetUserLogin(login)
}

func (oc *AIClient) GetApprovalHandler() agentremote.ApprovalReactionHandler {
	return oc.approvalFlow
}

const (
	openRouterAppReferer = "https://developers.beeper.com/agentremote"
	openRouterAppTitle   = "AI Chats for Beeper"
)

func openRouterHeaders() map[string]string {
	return map[string]string{
		"HTTP-Referer": openRouterAppReferer,
		"X-Title":      openRouterAppTitle,
	}
}

// initProviderForLogin creates the appropriate provider based on login metadata.
func initProviderForLogin(key string, meta *UserLoginMetadata, connector *OpenAIConnector, login *bridgev2.UserLogin, log zerolog.Logger) (*OpenAIProvider, error) {
	if meta == nil {
		return nil, errors.New("login metadata is required")
	}
	switch meta.Provider {
	case ProviderOpenRouter:
		pdfEngine := connector.Config.Providers.OpenRouter.DefaultPDFEngine
		return initOpenRouterProvider(key, connector.resolveOpenRouterBaseURL(), "", pdfEngine, ProviderOpenRouter, log)

	case ProviderMagicProxy:
		baseURL := normalizeProxyBaseURL(meta.BaseURL)
		if baseURL == "" {
			return nil, errors.New("magic proxy base_url is required")
		}
		pdfEngine := connector.Config.Providers.OpenRouter.DefaultPDFEngine
		return initOpenRouterProvider(key, joinProxyPath(baseURL, "/openrouter/v1"), "", pdfEngine, ProviderMagicProxy, log)

	case ProviderOpenAI:
		openaiURL := connector.resolveOpenAIBaseURL()
		log.Info().
			Str("provider", meta.Provider).
			Str("openai_url", openaiURL).
			Msg("Initializing AI provider endpoint")
		return NewOpenAIProviderWithBaseURL(key, openaiURL, log)

	default:
		return nil, fmt.Errorf("unsupported provider: %s", meta.Provider)
	}
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

func (oc *AIClient) acquireRoom(roomID id.RoomID) bool {
	oc.activeRoomsMu.Lock()
	defer oc.activeRoomsMu.Unlock()
	if oc.activeRooms[roomID] {
		return false // already processing
	}
	oc.activeRooms[roomID] = true
	return true
}

// releaseRoom releases a room after processing is complete.
func (oc *AIClient) releaseRoom(roomID id.RoomID) {
	oc.activeRoomsMu.Lock()
	defer oc.activeRoomsMu.Unlock()
	delete(oc.activeRooms, roomID)
	oc.clearRoomRun(roomID)
}

// queuePendingMessage adds a message to the pending queue for later processing.
func (oc *AIClient) queuePendingMessage(roomID id.RoomID, item pendingQueueItem, settings airuntime.QueueSettings) bool {
	enqueued := oc.enqueuePendingItem(roomID, item, settings)
	if enqueued {
		oc.startQueueTyping(oc.backgroundContext(context.Background()), item.pending.Portal, item.pending.Meta, item.pending.Typing)
	}
	return enqueued
}

func queueStatusEvents(primary *event.Event, extras []*event.Event) []*event.Event {
	events := make([]*event.Event, 0, 1+len(extras))
	seen := make(map[id.EventID]struct{}, 1+len(extras))
	appendEvent := func(evt *event.Event) {
		if evt == nil || evt.ID == "" {
			return
		}
		if _, exists := seen[evt.ID]; exists {
			return
		}
		seen[evt.ID] = struct{}{}
		events = append(events, evt)
	}
	appendEvent(primary)
	for _, evt := range extras {
		appendEvent(evt)
	}
	return events
}

func (oc *AIClient) sendQueueAcceptedSuccess(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, extras []*event.Event) {
	for _, statusEvt := range queueStatusEvents(evt, extras) {
		oc.sendSuccessStatus(ctx, portal, statusEvt)
	}
}

func (oc *AIClient) sendQueueRejectedStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, extras []*event.Event, reason string) {
	if portal == nil || portal.Bridge == nil {
		return
	}
	message := strings.TrimSpace(reason)
	if message == "" {
		message = "Couldn't queue the message. Try again."
	}
	err := fmt.Errorf("%s", message)
	msgStatus := bridgev2.WrapErrorInStatus(err).
		WithStatus(event.MessageStatusRetriable).
		WithErrorReason(event.MessageStatusGenericError).
		WithMessage(message).
		WithIsCertain(true).
		WithSendNotice(false)
	for _, statusEvt := range queueStatusEvents(evt, extras) {
		portal.Bridge.Matrix.SendMessageStatus(ctx, &msgStatus, bridgev2.StatusEventInfoFromEvent(statusEvt))
	}
}

// saveUserMessage persists a user message to the database.
func (oc *AIClient) saveUserMessage(ctx context.Context, evt *event.Event, msg *database.Message) {
	if evt != nil {
		msg.MXID = evt.ID
	}
	ensureCanonicalUserMessage(msg)
	if _, err := oc.UserLogin.Bridge.GetGhostByID(ctx, msg.SenderID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure user ghost before saving message")
	}
	if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, msg); err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to save message to database")
	}
}

// dispatchOrQueueCore contains shared dispatch/steer/queue logic.
// When userMessage is non-nil, it saves the message to the DB, handles ack
// reactions, sends pending status on acquire, and notifies session mutations.
// Returns true if the message was accepted (dispatched or queued).
func (oc *AIClient) dispatchOrQueueCore(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	userMessage *database.Message,
	queueItem pendingQueueItem,
	queueSettings airuntime.QueueSettings,
	promptContext PromptContext,
) bool {
	roomID := portal.MXID
	behavior := airuntime.ResolveQueueBehavior(queueSettings.Mode)
	shouldSteer := behavior.Steer
	shouldFollowup := behavior.Followup
	hasDBMessage := userMessage != nil
	queueDecision := airuntime.DecideQueueAction(queueSettings.Mode, oc.roomHasActiveRun(roomID), false)
	if queueDecision.Action == airuntime.QueueActionInterruptAndRun {
		oc.cancelRoomRun(roomID)
		oc.clearPendingQueue(roomID)
	}
	if oc.acquireRoom(roomID) {
		oc.stopQueueTyping(roomID)
		if hasDBMessage {
			oc.saveUserMessage(ctx, evt, userMessage)
		}
		if evt != nil && !queueItem.pending.PendingSent {
			oc.sendPendingStatus(ctx, portal, evt, "Processing...")
			queueItem.pending.PendingSent = true
		}
		runCtx := oc.backgroundContext(ctx)
		if len(queueItem.pending.StatusEvents) > 0 {
			runCtx = context.WithValue(runCtx, statusEventsKey{}, queueItem.pending.StatusEvents)
		}
		if queueItem.pending.InboundContext != nil {
			runCtx = withInboundContext(runCtx, *queueItem.pending.InboundContext)
		}
		if queueItem.pending.Typing != nil {
			runCtx = WithTypingContext(runCtx, queueItem.pending.Typing)
		}
		runCtx = oc.attachRoomRun(runCtx, roomID)
		metaSnapshot := clonePortalMetadata(meta)
		go func(metaSnapshot *PortalMetadata) {
			defer func() {
				if hasDBMessage && metaSnapshot != nil && metaSnapshot.AckReactionRemoveAfter {
					oc.removePendingAckReactions(oc.backgroundContext(ctx), portal, queueItem.pending)
				}
				oc.releaseRoom(roomID)
				oc.processPendingQueue(oc.backgroundContext(ctx), roomID)
			}()
			oc.dispatchCompletionInternal(runCtx, evt, portal, metaSnapshot, promptContext)
		}(metaSnapshot)
		if hasDBMessage {
			oc.notifySessionMutation(ctx, portal, meta, false)
		}
		return true
	}

	messageSaved := false
	if shouldSteer && queueItem.pending.Type == pendingTypeText {
		queueItem.prompt = queueItem.pending.MessageBody
		steered := oc.enqueueSteerQueue(roomID, queueItem)
		if steered {
			if hasDBMessage {
				oc.saveUserMessage(ctx, evt, userMessage)
				messageSaved = true
			}
			if !shouldFollowup {
				if evt != nil && !queueItem.pending.PendingSent {
					oc.sendPendingStatus(ctx, portal, evt, "Processing...")
					queueItem.pending.PendingSent = true
				}
				if hasDBMessage {
					oc.notifySessionMutation(ctx, portal, meta, false)
				}
				return true
			}
		}
	}

	// Room busy - queue for later
	if behavior.BacklogAfter {
		queueItem.backlogAfter = true
	}
	enqueued := oc.queuePendingMessage(roomID, queueItem, queueSettings)
	if !enqueued {
		oc.sendQueueRejectedStatus(ctx, portal, evt, queueItem.pending.StatusEvents, "Couldn't queue the message. Try again.")
		return false
	}
	oc.sendQueueAcceptedSuccess(ctx, portal, evt, queueItem.pending.StatusEvents)
	if hasDBMessage && !messageSaved {
		oc.saveUserMessage(ctx, evt, userMessage)
	}
	if hasDBMessage {
		oc.notifySessionMutation(ctx, portal, meta, false)
	}
	return true
}

func (oc *AIClient) dispatchOrQueue(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	userMessage *database.Message,
	queueItem pendingQueueItem,
	queueSettings airuntime.QueueSettings,
	promptContext PromptContext,
) (dbMessage *database.Message, isPending bool) {
	isPending = oc.dispatchOrQueueCore(ctx, evt, portal, meta, userMessage, queueItem, queueSettings, promptContext)
	return userMessage, isPending
}

// processPendingQueue processes queued messages for a room.
func (oc *AIClient) processPendingQueue(ctx context.Context, roomID id.RoomID) {
	if oc == nil || roomID == "" {
		return
	}
	if !oc.markQueueDraining(roomID) {
		return
	}

	go func() {
		defer oc.clearQueueDraining(roomID)
		snapshot := oc.getQueueSnapshot(roomID)
		if snapshot == nil || (len(snapshot.items) == 0 && snapshot.droppedCount == 0) {
			return
		}
		// Wait for debounce window to pass since last enqueue.
		if snapshot.debounceMs > 0 {
			for {
				current := oc.getQueueSnapshot(roomID)
				if current == nil {
					return
				}
				since := time.Now().UnixMilli() - current.lastEnqueuedAt
				if since >= int64(current.debounceMs) {
					break
				}
				wait := current.debounceMs - int(since)
				if wait < 0 {
					wait = 0
				}
				time.Sleep(time.Duration(wait) * time.Millisecond)
			}
		}

		if !oc.acquireRoom(roomID) {
			return
		}
		oc.stopQueueTyping(roomID)

		candidate, actionSnapshot := oc.takePendingQueueDispatchCandidate(roomID, false)
		if actionSnapshot == nil || candidate == nil || len(candidate.items) == 0 {
			oc.releaseRoom(roomID)
			return
		}

		item, prompt, ok := preparePendingQueueDispatchCandidate(candidate)
		if !ok {
			oc.releaseRoom(roomID)
			return
		}

		var promptContext PromptContext
		var err error

		metaSnapshot := clonePortalMetadata(item.pending.Meta)
		var eventID id.EventID
		if item.pending.Event != nil {
			eventID = item.pending.Event.ID
		}
		promptCtx := ctx
		if item.pending.InboundContext != nil {
			promptCtx = withInboundContext(promptCtx, *item.pending.InboundContext)
		}
		switch item.pending.Type {
		case pendingTypeText:
			promptContext, err = oc.buildContextWithLinkContext(promptCtx, item.pending.Portal, metaSnapshot, prompt, item.rawEventContent, eventID)
		case pendingTypeImage, pendingTypePDF, pendingTypeAudio, pendingTypeVideo:
			promptContext, err = oc.buildContextWithMedia(promptCtx, item.pending.Portal, metaSnapshot, item.pending.MessageBody, item.pending.MediaURL, item.pending.MimeType, item.pending.EncryptedFile, item.pending.Type, eventID)
		case pendingTypeRegenerate:
			promptContext, err = oc.buildContextForRegenerate(promptCtx, item.pending.Portal, metaSnapshot, item.pending.MessageBody, item.pending.SourceEventID)
		case pendingTypeEditRegenerate:
			promptContext, err = oc.buildContextUpToMessage(promptCtx, item.pending.Portal, metaSnapshot, item.pending.TargetMsgID, item.pending.MessageBody)
		default:
			err = fmt.Errorf("unknown pending message type: %s", item.pending.Type)
		}

		if err != nil {
			oc.loggerForContext(ctx).Err(err).Msg("Failed to build prompt for pending queue item")
			oc.notifyMatrixSendFailure(ctx, item.pending.Portal, item.pending.Event, err)
			if item.pending.Meta != nil && item.pending.Meta.AckReactionRemoveAfter {
				oc.removePendingAckReactions(oc.backgroundContext(ctx), item.pending.Portal, item.pending)
			}
			oc.releaseRoom(roomID)
			oc.processPendingQueue(oc.backgroundContext(ctx), roomID)
			return
		}

		oc.dispatchQueuedPrompt(ctx, item, promptContext)
	}()
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

	// Trust the token - auth errors will be caught during actual API usage
	// OpenRouter and Beeper provider don't support the GET /v1/models/{model} endpoint
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
	oc.SetLoggedIn(false)

	oc.stopLifecycleIntegrations()
	// Stop all login-scoped integration workers for this login.
	if oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.DB != nil {
		bridgeID := string(oc.UserLogin.Bridge.DB.BridgeID)
		loginID := string(oc.UserLogin.ID)
		oc.stopLoginLifecycleIntegrations(bridgeID, loginID)
	}

	// Clean up per-room maps to prevent unbounded growth
	oc.activeRoomsMu.Lock()
	clear(oc.activeRooms)
	oc.activeRoomsMu.Unlock()

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
	// Best-effort: remove per-login data not covered by bridgev2's user_login/portal/message cleanup.
	if oc != nil && oc.UserLogin != nil {
		purgeLoginDataBestEffort(ctx, oc.UserLogin)
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
	meta := portalMeta(portal)
	return agentremote.BuildChatInfoWithFallback(meta.Title, portal.Name, "AI Chat", portal.Topic), nil
}

func (oc *AIClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	ghostID := string(ghost.ID)

	// Parse agent from ghost ID (format: "agent-{id}")
	if agentID, ok := parseAgentFromGhostID(ghostID); ok {
		store := NewAgentStoreAdapter(oc)
		agent, err := store.GetAgentByID(ctx, agentID)
		if err == nil && agent != nil {
			if sdkAgent := oc.sdkAgentForDefinition(ctx, agent); sdkAgent != nil {
				info := sdkAgent.UserInfo()
				info.ExtraUpdates = updateGhostLastSync
				return info, nil
			}
		}
		return &bridgev2.UserInfo{
			Name:         ptr.Ptr("Unknown Agent"),
			IsBot:        ptr.Ptr(true),
			Identifiers:  stringutil.DedupeStrings([]string{agentID}),
			ExtraUpdates: updateGhostLastSync,
		}, nil
	}

	// Parse model from ghost ID (format: "model-{escaped-model-id}")
	if modelID := parseModelFromGhostID(ghostID); modelID != "" {
		info := oc.findModelInfo(modelID)
		return &bridgev2.UserInfo{
			Name:         ptr.Ptr(modelContactName(modelID, info)),
			IsBot:        ptr.Ptr(false),
			Identifiers:  modelContactIdentifiers(modelID, info),
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
		caps.Edit = event.CapLevelFullySupported
		caps.EditMaxCount = 10
		caps.EditMaxAge = ptr.Ptr(jsontime.S(AIEditMaxAge))
		caps.Reaction = event.CapLevelFullySupported
		caps.ReactionCount = 1
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
	if meta != nil && strings.TrimSpace(meta.RuntimeModelOverride) != "" {
		return ResolveAlias(meta.RuntimeModelOverride)
	}
	if meta != nil && meta.ResolvedTarget != nil {
		switch meta.ResolvedTarget.Kind {
		case ResolvedTargetModel:
			return ResolveAlias(meta.ResolvedTarget.ModelID)
		case ResolvedTargetAgent:
			store := NewAgentStoreAdapter(oc)
			agent, err := store.GetAgentByID(context.Background(), meta.ResolvedTarget.AgentID)
			if err == nil && agent != nil && agent.Model.Primary != "" {
				return ResolveAlias(agent.Model.Primary)
			}
			return ""
		default:
			return ""
		}
	}
	return oc.defaultModelForProvider()
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
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta == nil {
		return DefaultModelOpenRouter
	}
	providers := oc.connector.Config.Providers

	switch loginMeta.Provider {
	case ProviderOpenAI:
		if providers.OpenAI.DefaultModel != "" {
			return providers.OpenAI.DefaultModel
		}
		return DefaultModelOpenAI
	case ProviderOpenRouter, ProviderMagicProxy:
		if providers.OpenRouter.DefaultModel != "" {
			return providers.OpenRouter.DefaultModel
		}
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
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta == nil {
		return strings.TrimSpace(oc.gravatarContext())
	}

	var lines []string
	if profile := loginMeta.Profile; profile != nil {
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

	if connectorConfig.LinkPreviews != nil {
		cfg := connectorConfig.LinkPreviews
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

	modelCaps := oc.getModelCapabilitiesForMeta(meta)

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
	if caps := oc.getModelCapabilitiesForMeta(meta); caps.SupportsReasoning {
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
	if !oc.getModelCapabilitiesForMeta(meta).SupportsReasoning {
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
	loginMeta := loginMetadata(oc.UserLogin)
	return loginMeta.Provider == ProviderOpenRouter || loginMeta.Provider == ProviderMagicProxy
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

// effectivePDFEngine returns the PDF engine to use for the given portal.
// Priority: room-level PDFConfig > provider-level config > default "mistral-ocr"
func (oc *AIClient) effectivePDFEngine(meta *PortalMetadata) string {
	// Room-level override
	if meta != nil && meta.PDFConfig != nil && meta.PDFConfig.Engine != "" {
		return meta.PDFConfig.Engine
	}

	// Provider-level config
	loginMeta := loginMetadata(oc.UserLogin)
	switch loginMeta.Provider {
	case ProviderOpenRouter, ProviderMagicProxy:
		if engine := oc.connector.Config.Providers.OpenRouter.DefaultPDFEngine; engine != "" {
			return engine
		}
	}

	return "mistral-ocr" // Default
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

// listAvailableModels fetches models from OpenAI API and caches them
// Returns ModelInfo list from the provider
func (oc *AIClient) listAvailableModels(ctx context.Context, forceRefresh bool) ([]ModelInfo, error) {
	meta := loginMetadata(oc.UserLogin)

	// Check cache (refresh every 6 hours unless forced)
	if !forceRefresh && meta.ModelCache != nil {
		age := time.Now().Unix() - meta.ModelCache.LastRefresh
		if age < meta.ModelCache.CacheDuration {
			return meta.ModelCache.Models, nil
		}
	}

	oc.loggerForContext(ctx).Debug().Msg("Loading derived model catalog")
	allModels := oc.loadModelCatalogModels(ctx)

	// Update cache
	if meta.ModelCache == nil {
		meta.ModelCache = &ModelCache{
			CacheDuration: int64(oc.connector.Config.ModelCacheDuration.Seconds()),
		}
	}
	meta.ModelCache.Models = allModels
	meta.ModelCache.LastRefresh = time.Now().Unix()

	// Save metadata
	if err := oc.UserLogin.Save(ctx); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to save model cache")
	}

	oc.loggerForContext(ctx).Info().Int("count", len(allModels)).Msg("Cached available models")
	return allModels, nil
}

// findModelInfo looks up ModelInfo from the user's model cache by ID
func (oc *AIClient) findModelInfo(modelID string) *ModelInfo {
	meta := loginMetadata(oc.UserLogin)
	if meta != nil && meta.ModelCache != nil {
		for i := range meta.ModelCache.Models {
			if meta.ModelCache.Models[i].ID == modelID {
				return &meta.ModelCache.Models[i]
			}
		}
	}
	return oc.findModelInfoInCatalog(modelID)
}

// maxHistoryImageMessages limits how many recent history messages can have images injected,
// to keep token usage under control.
const maxHistoryImageMessages = 10

// isImageMimeType returns true if the MIME type is an image format suitable for vision models.
func isImageMimeType(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

// updateAssistantGeneratedFiles finds the most recent assistant message with tool calls
// in the portal and appends the given GeneratedFileRef entries to its metadata.
// This is used by async image generation to link generated images back to the assistant
// turn that triggered them, so the model can reference them via [media_url: ...] in history.
func (oc *AIClient) updateAssistantGeneratedFiles(ctx context.Context, portal *bridgev2.Portal, refs []GeneratedFileRef) {
	if len(refs) == 0 {
		return
	}
	messages, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, 10)
	if err != nil {
		oc.Log().Warn().Err(err).Msg("Failed to load messages for async GeneratedFiles update")
		return
	}
	for _, msg := range messages {
		meta, ok := msg.Metadata.(*MessageMetadata)
		if !ok || meta.Role != "assistant" || !meta.HasToolCalls {
			continue
		}
		// Found the most recent assistant message with tool calls — update its GeneratedFiles.
		meta.GeneratedFiles = append(meta.GeneratedFiles, refs...)
		if err := oc.UserLogin.Bridge.DB.Message.Update(ctx, msg); err != nil {
			oc.Log().Warn().Err(err).Str("msg_id", string(msg.ID)).Msg("Failed to update assistant message with async GeneratedFiles")
		} else {
			oc.Log().Debug().Str("msg_id", string(msg.ID)).Int("files", len(refs)).Msg("Updated assistant message with async GeneratedFiles")
		}
		return
	}
	oc.Log().Warn().Msg("No assistant message found to update with async GeneratedFiles")
}

func (oc *AIClient) promptContextToDispatchMessages(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	promptContext PromptContext,
) []openai.ChatCompletionMessageParamUnion {
	promptMessages := bridgesdk.PromptContextToChatCompletionMessages(promptContext.PromptContext, oc.isOpenRouterProvider())
	promptMessages = oc.augmentPromptWithIntegrations(ctx, portal, meta, promptMessages)
	if meta != nil && IsGoogleModel(oc.effectiveModel(meta)) {
		promptMessages = SanitizeGoogleTurnOrdering(promptMessages)
	}
	return promptMessages
}

type historyLoadResult struct {
	rows      []*database.Message
	hasVision bool
	resetAt   int64
}

// fetchHistoryRows resolves the history limit, extracts the resetAt cutoff,
// and fetches the last N messages. Returns nil when the history limit is zero.
func (oc *AIClient) fetchHistoryRows(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) (*historyLoadResult, error) {
	historyLimit := oc.historyLimit(ctx, portal, meta)
	if historyLimit <= 0 {
		return nil, nil
	}
	resetAt := int64(0)
	if meta != nil {
		resetAt = meta.SessionResetAt
	}
	history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, historyLimit)
	if err != nil {
		return nil, fmt.Errorf("failed to load prompt history: %w", err)
	}
	return &historyLoadResult{
		rows:      history,
		hasVision: oc.getModelCapabilitiesForMeta(meta).SupportsVision,
		resetAt:   resetAt,
	}, nil
}

func (oc *AIClient) loadHistoryMessages(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) ([]PromptMessage, error) {
	hr, err := oc.fetchHistoryRows(ctx, portal, meta)
	if err != nil {
		return nil, err
	}
	if hr == nil {
		return nil, nil
	}
	var messages []PromptMessage
	for i := len(hr.rows) - 1; i >= 0; i-- {
		msgMeta := messageMeta(hr.rows[i])
		if !shouldIncludeInHistory(msgMeta) {
			continue
		}
		if hr.resetAt > 0 && hr.rows[i].Timestamp.UnixMilli() < hr.resetAt {
			continue
		}
		injectImages := hr.hasVision && i < maxHistoryImageMessages
		messages = append(messages, oc.historyMessageBundle(ctx, msgMeta, injectImages)...)
	}
	return messages, nil
}

func (oc *AIClient) buildBaseContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) (PromptContext, error) {
	var promptContext PromptContext
	bridgesdk.AppendChatMessagesToPromptContext(&promptContext.PromptContext, maybePrependSessionGreeting(ctx, portal, meta, nil, oc.log))
	bridgesdk.AppendChatMessagesToPromptContext(&promptContext.PromptContext, oc.buildSystemMessages(ctx, portal, meta))

	historyMessages, err := oc.loadHistoryMessages(ctx, portal, meta)
	if err != nil {
		return PromptContext{}, err
	}
	promptContext.Messages = append(promptContext.Messages, historyMessages...)

	return promptContext, nil
}

func (oc *AIClient) buildBasePrompt(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) ([]openai.ChatCompletionMessageParamUnion, error) {
	promptContext, err := oc.buildBaseContext(ctx, portal, meta)
	if err != nil {
		return nil, err
	}
	return oc.promptContextToDispatchMessages(ctx, portal, meta, promptContext), nil
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
// appends the meta system prompt, resolves body overrides, and applies the abort hint.
// Callers must call applyUntrustedPrefix at the appropriate point in message assembly.
func (oc *AIClient) prepareInboundPromptContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	userText string,
	eventID id.EventID,
) (inboundPromptResult, error) {
	promptContext, err := oc.buildBaseContext(ctx, portal, meta)
	if err != nil {
		return inboundPromptResult{}, err
	}
	inboundCtx := oc.resolvePromptInboundContext(ctx, portal, userText, eventID)
	bridgesdk.AppendPromptText(&promptContext.SystemPrompt, airuntime.BuildInboundMetaSystemPrompt(inboundCtx))

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

func (r *inboundPromptResult) applyUntrustedPrefix() {
	if r.UntrustedPrefix != "" {
		r.ResolvedBody = r.UntrustedPrefix + "\n\n" + r.ResolvedBody
	}
}

func (oc *AIClient) buildContextWithLinkContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	latest string,
	rawEventContent map[string]any,
	eventID id.EventID,
) (PromptContext, error) {
	result, err := oc.prepareInboundPromptContext(ctx, portal, meta, latest, eventID)
	if err != nil {
		return PromptContext{}, err
	}

	if linkContext := oc.buildLinkContext(ctx, latest, rawEventContent); linkContext != "" {
		result.ResolvedBody += linkContext
	}

	if portal != nil && portal.MXID != "" {
		reactionFeedback := DrainReactionFeedback(portal.MXID)
		if len(reactionFeedback) > 0 {
			if feedbackText := FormatReactionFeedback(reactionFeedback); feedbackText != "" {
				result.ResolvedBody = feedbackText + "\n" + result.ResolvedBody
			}
		}
	}

	result.applyUntrustedPrefix()

	result.PromptContext.Messages = append(result.PromptContext.Messages, PromptMessage{
		Role: PromptRoleUser,
		Blocks: []PromptBlock{{
			Type: PromptBlockText,
			Text: result.ResolvedBody,
		}},
	})
	return result.PromptContext, nil
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

// buildPromptWithMedia builds a prompt with media content.
func (oc *AIClient) buildContextWithMedia(
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
	result, err := oc.prepareInboundPromptContext(ctx, portal, meta, caption, eventID)
	if err != nil {
		return PromptContext{}, err
	}
	result.applyUntrustedPrefix()
	blocks := make([]PromptBlock, 0, 2)
	if strings.TrimSpace(result.ResolvedBody) != "" {
		blocks = append(blocks, PromptBlock{Type: PromptBlockText, Text: result.ResolvedBody})
	}

	switch mediaType {
	case pendingTypeImage:
		b64Data, actualMimeType, err := oc.downloadMediaBase64(ctx, mediaURL, encryptedFile, 20, mimeType) // 20MB limit for images
		if err != nil {
			return PromptContext{}, fmt.Errorf("failed to download image: %w", err)
		}
		blocks = append(blocks, PromptBlock{
			Type:     PromptBlockImage,
			ImageB64: b64Data,
			MimeType: actualMimeType,
		})

	case pendingTypePDF:
		b64Data, actualMimeType, err := oc.downloadMediaBase64(ctx, mediaURL, encryptedFile, 50, mimeType) // 50MB limit
		if err != nil {
			return PromptContext{}, fmt.Errorf("failed to download PDF: %w", err)
		}
		if actualMimeType == "" {
			actualMimeType = "application/pdf"
		}
		blocks = append(blocks, PromptBlock{
			Type:     PromptBlockFile,
			FileB64:  bridgesdk.BuildDataURL(actualMimeType, b64Data),
			Filename: "document.pdf",
			MimeType: actualMimeType,
		})

	case pendingTypeAudio:
		if strings.TrimSpace(result.ResolvedBody) == "" {
			blocks = append(blocks, PromptBlock{
				Type: PromptBlockText,
				Text: fmt.Sprintf("Audio attachment: %s", mediaURL),
			})
		}

	case pendingTypeVideo:
		if strings.TrimSpace(result.ResolvedBody) == "" {
			blocks = append(blocks, PromptBlock{
				Type: PromptBlockText,
				Text: fmt.Sprintf("Video attachment: %s", mediaURL),
			})
		}

	default:
		return PromptContext{}, fmt.Errorf("unsupported media type: %s", mediaType)
	}
	result.PromptContext.Messages = append(result.PromptContext.Messages, PromptMessage{
		Role:   PromptRoleUser,
		Blocks: blocks,
	})
	return result.PromptContext, nil
}

// buildPromptUpToMessage builds a prompt including messages up to and including the specified message
func (oc *AIClient) buildContextUpToMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	targetMessageID networkid.MessageID,
	newBody string,
) (PromptContext, error) {
	var promptContext PromptContext
	bridgesdk.AppendChatMessagesToPromptContext(&promptContext.PromptContext, oc.buildSystemMessages(ctx, portal, meta))

	hr, err := oc.fetchHistoryRows(ctx, portal, meta)
	if err != nil {
		return PromptContext{}, err
	}
	if hr != nil {
		// Add messages up to the target message, replacing the target with newBody
		for i := len(hr.rows) - 1; i >= 0; i-- {
			msg := hr.rows[i]
			msgMeta := messageMeta(msg)

			// Stop after adding the target message
			if msg.ID == targetMessageID {
				body := newBody
				promptContext.Messages = append(promptContext.Messages, PromptMessage{
					Role: PromptRoleUser,
					Blocks: []PromptBlock{{
						Type: PromptBlockText,
						Text: body,
					}},
				})
				break
			}

			if !shouldIncludeInHistory(msgMeta) {
				continue
			}
			if hr.resetAt > 0 && msg.Timestamp.UnixMilli() < hr.resetAt {
				continue
			}

			injectImages := hr.hasVision && i < maxHistoryImageMessages
			promptContext.Messages = append(promptContext.Messages, oc.historyMessageBundle(ctx, msgMeta, injectImages)...)
		}
	} else {
		body := strings.TrimSpace(newBody)
		body = airuntime.SanitizeChatMessageForDisplay(body, true)
		promptContext.Messages = append(promptContext.Messages, PromptMessage{
			Role: PromptRoleUser,
			Blocks: []PromptBlock{{
				Type: PromptBlockText,
				Text: body,
			}},
		})
	}

	return promptContext, nil
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

// getAudioFormat extracts the audio format from a MIME type for OpenRouter API
func getAudioFormat(mimeType string) string {
	switch mimeType {
	case "audio/wav", "audio/x-wav":
		return "wav"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/webm":
		return "webm"
	case "audio/ogg":
		return "ogg"
	case "audio/flac":
		return "flac"
	case "audio/mp4", "audio/x-m4a":
		return "mp4"
	default:
		// Default to mp3 for unknown formats
		return "mp3"
	}
}

// ensureGhostDisplayName ensures the ghost has its display name set before sending messages.
// This fixes the issue where ghosts appear with raw user IDs instead of formatted names.
func (oc *AIClient) ensureGhostDisplayName(ctx context.Context, modelID string) {
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
			Identifiers: modelContactIdentifiers(modelID, info),
		})
		oc.loggerForContext(ctx).Debug().Str("model", modelID).Str("name", displayName).Msg("Updated ghost display name")
	}
}

// ensureAgentGhostDisplayName ensures the agent ghost has its display name set.
func (oc *AIClient) ensureAgentGhostDisplayName(ctx context.Context, agentID, modelID, agentName string) {
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
			Identifiers: agentContactIdentifiers(agentID, modelID, oc.findModelInfo(modelID)),
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
	return agentremote.LoggerFromContext(ctx, &oc.log)
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

// getModelCapabilities computes capabilities for a model.
// If info is provided, it uses the ModelInfo fields for accurate capability detection.
// If info is missing, capabilities default to false (except tool calling).
func getModelCapabilities(modelID string, info *ModelInfo) ModelCapabilities {
	caps := ModelCapabilities{
		SupportsToolCalling: true, // Default true, overridden by ModelInfo if available
	}

	// Use ModelInfo if available (more accurate than heuristics)
	if info != nil {
		caps.SupportsVision = info.SupportsVision
		caps.SupportsPDF = info.SupportsPDF
		caps.SupportsImageGen = info.SupportsImageGen
		caps.SupportsToolCalling = info.SupportsToolCalling
		caps.SupportsAudio = info.SupportsAudio
		caps.SupportsVideo = info.SupportsVideo
		caps.SupportsReasoning = info.SupportsReasoning
	}

	return caps
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
		rawEventContent = last.Event.Content.Raw
	}

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
	promptContext, err := oc.buildContextWithLinkContext(statusCtx, last.Portal, last.Meta, combinedBody, rawEventContent, last.Event.ID)
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
		ID:       agentremote.MatrixMessageID(last.Event.ID),
		MXID:     last.Event.ID,
		Room:     last.Portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			BaseMessageMetadata: agentremote.BaseMessageMetadata{Role: "user", Body: combinedBody},
		},
		Timestamp: agentremote.MatrixEventTimestamp(last.Event),
	}
	setCanonicalTurnDataFromPromptMessages(userMessage.Metadata.(*MessageMetadata), promptTail(promptContext, 1))
	ensureCanonicalUserMessage(userMessage)

	// Save user message to database - we must do this ourselves since we already
	// returned Pending: true to the bridge framework when debouncing started
	// Ensure ghost row exists to avoid foreign key violations.
	if _, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userMessage.SenderID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure user ghost before saving debounced message")
	}
	if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, userMessage); err != nil {
		oc.loggerForContext(ctx).Err(err).Msg("Failed to save debounced user message to database")
	}

	// Dispatch using existing flow (handles room lock + status)
	// Pass nil for userMessage since we already saved it above
	ackRemoveIDs := make([]id.EventID, 0, len(entries))
	for _, entry := range entries {
		if entry.Event != nil {
			ackRemoveIDs = append(ackRemoveIDs, entry.Event.ID)
		}
	}

	pending := pendingMessage{
		Event:           last.Event,
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
		messageID:       string(last.Event.ID),
		summaryLine:     combinedRaw,
		enqueuedAt:      time.Now().UnixMilli(),
		rawEventContent: rawEventContent,
	}
	queueSettings, _, _, _ := oc.resolveQueueSettingsForPortal(statusCtx, last.Portal, last.Meta, "", airuntime.QueueInlineOptions{})

	_, _ = oc.dispatchOrQueue(statusCtx, last.Event, last.Portal, last.Meta, nil, queueItem, queueSettings, promptContext)

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
