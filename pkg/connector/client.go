package connector

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/rs/zerolog"
	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/ptr"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/ai-bridge/pkg/agents"
	"github.com/beeper/ai-bridge/pkg/cron"
)

var (
	_ bridgev2.NetworkAPI                       = (*AIClient)(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI    = (*AIClient)(nil)
	_ bridgev2.ContactListingNetworkAPI         = (*AIClient)(nil)
	_ bridgev2.UserSearchingNetworkAPI          = (*AIClient)(nil)
	_ bridgev2.EditHandlingNetworkAPI           = (*AIClient)(nil)
	_ bridgev2.ReactionHandlingNetworkAPI       = (*AIClient)(nil)
	_ bridgev2.RedactionHandlingNetworkAPI      = (*AIClient)(nil)
	_ bridgev2.DisappearTimerChangingNetworkAPI = (*AIClient)(nil)
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

// AI bridge capability constants
const (
	AIMaxTextLength        = 100000
	AIEditMaxAge           = 24 * time.Hour
	modelValidationTimeout = 5 * time.Second
)

func aiCapID() string {
	return "com.beeper.ai.capabilities.2025_01_31"
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

// buildCapabilityID constructs a deterministic capability ID based on model modalities.
// Suffixes are sorted alphabetically to ensure the same capabilities produce the same ID.
func buildCapabilityID(caps ModelCapabilities) string {
	var suffixes []string

	// Add suffixes in alphabetical order for determinism
	if caps.SupportsAudio {
		suffixes = append(suffixes, "audio")
	}
	if caps.SupportsImageGen {
		suffixes = append(suffixes, "imagegen")
	}
	if caps.SupportsPDF {
		suffixes = append(suffixes, "pdf")
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
		MimeTypes:        textFileMimeTypes(),
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
	UserLogin *bridgev2.UserLogin
	connector *OpenAIConnector
	api       openai.Client
	apiKey    string
	log       zerolog.Logger

	// Provider abstraction layer - all providers use OpenAI SDK
	provider AIProvider

	loggedIn      atomic.Bool
	chatLock      sync.Mutex
	bootstrapOnce sync.Once // Ensures bootstrap only runs once per client instance

	// Turn-based message queuing: only one response per room at a time
	activeRooms   map[id.RoomID]bool
	activeRoomsMu sync.Mutex

	// Pending message queue per room (for turn-based behavior)
	pendingMessages   map[id.RoomID][]pendingMessage
	pendingMessagesMu sync.Mutex

	// Subagent runs (sessions_spawn)
	subagentRuns   map[string]*subagentRun
	subagentRunsMu sync.Mutex

	// Compactor handles intelligent context compaction with LLM summarization
	compactor     *Compactor
	compactorOnce sync.Once

	// Message deduplication cache
	inboundDedupeCache *DedupeCache

	// Message debouncer for combining rapid messages
	inboundDebouncer *Debouncer

	// Cron + heartbeat
	cronService     *cron.CronService
	heartbeatRunner *HeartbeatRunner
	heartbeatWake   *HeartbeatWake

	// Model catalog cache (VFS-backed)
	modelCatalogMu     sync.Mutex
	modelCatalogLoaded bool
	modelCatalogCache  []ModelCatalogEntry
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
	Event         *event.Event
	Portal        *bridgev2.Portal
	Meta          *PortalMetadata
	Type          pendingMessageType
	MessageBody   string                   // For text, regenerate, edit_regenerate (caption for media)
	MediaURL      string                   // For media messages (image, PDF, audio, video)
	MimeType      string                   // MIME type of the media
	EncryptedFile *event.EncryptedFileInfo // For encrypted Matrix media (E2EE rooms)
	TargetMsgID   networkid.MessageID      // For edit_regenerate
	SourceEventID id.EventID               // For regenerate (original user message ID)
	StatusEvents  []*event.Event           // Extra events to mark sent when processing starts
	PendingSent   bool                     // Whether a pending status was already sent for this event
}

func newAIClient(login *bridgev2.UserLogin, connector *OpenAIConnector, apiKey string) (*AIClient, error) {
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return nil, fmt.Errorf("missing API key")
	}

	// Get per-user credentials from login metadata
	meta := login.Metadata.(*UserLoginMetadata)
	log := login.Log.With().Str("component", "ai-network").Str("provider", meta.Provider).Logger()

	// Create base client struct
	oc := &AIClient{
		UserLogin:       login,
		connector:       connector,
		apiKey:          key,
		log:             log,
		activeRooms:     make(map[id.RoomID]bool),
		pendingMessages: make(map[id.RoomID][]pendingMessage),
		subagentRuns:    make(map[string]*subagentRun),
	}

	// Initialize inbound message processing with config values
	inboundCfg := connector.Config.Inbound.WithDefaults()
	oc.inboundDedupeCache = NewDedupeCache(inboundCfg.DedupeTTL, inboundCfg.DedupeMaxSize)
	oc.inboundDebouncer = NewDebouncer(inboundCfg.DefaultDebounceMs, oc.handleDebouncedMessages, func(err error, entries []DebounceEntry) {
		log.Warn().Err(err).Int("entries", len(entries)).Msg("Debounce flush failed")
	})

	// Initialize provider based on login metadata
	// All providers use the OpenAI SDK with different base URLs
	switch meta.Provider {
	case ProviderBeeper:
		// Beeper mode: routes through Beeper's OpenRouter proxy
		beeperBaseURL := connector.resolveBeeperBaseURL(meta)
		if beeperBaseURL == "" {
			return nil, fmt.Errorf("beeper base_url is required for Beeper provider")
		}

		// Get user ID for rate limiting
		userID := login.User.MXID.String()

		openrouterURL := beeperBaseURL + "/openrouter/v1"

		// Get PDF engine from provider config
		pdfEngine := connector.Config.Providers.Beeper.DefaultPDFEngine
		if pdfEngine == "" {
			pdfEngine = "mistral-ocr" // Default
		}

		headers := openRouterHeaders()
		provider, err := NewOpenAIProviderWithPDFPlugin(key, openrouterURL, userID, pdfEngine, headers, log)
		if err != nil {
			return nil, fmt.Errorf("failed to create Beeper provider: %w", err)
		}
		oc.provider = provider
		oc.api = provider.Client()

	case ProviderOpenRouter:
		// OpenRouter direct access
		openrouterURL := connector.resolveOpenRouterBaseURL()

		// Get PDF engine from provider config
		pdfEngine := connector.Config.Providers.OpenRouter.DefaultPDFEngine
		if pdfEngine == "" {
			pdfEngine = "mistral-ocr" // Default
		}

		headers := openRouterHeaders()
		provider, err := NewOpenAIProviderWithPDFPlugin(key, openrouterURL, "", pdfEngine, headers, log)
		if err != nil {
			return nil, fmt.Errorf("failed to create OpenRouter provider: %w", err)
		}
		oc.provider = provider
		oc.api = provider.Client()

	default:
		// OpenAI (default) or Custom OpenAI-compatible provider
		openaiURL := connector.resolveOpenAIBaseURL()
		provider, err := NewOpenAIProviderWithBaseURL(key, openaiURL, log)
		if err != nil {
			return nil, fmt.Errorf("failed to create OpenAI provider: %w", err)
		}
		oc.provider = provider
		oc.api = provider.Client()
	}

	oc.heartbeatWake = &HeartbeatWake{}
	oc.heartbeatRunner = NewHeartbeatRunner(oc)
	oc.cronService = oc.buildCronService()

	return oc, nil
}

const (
	openRouterAppReferer = "https://developers.beeper.com/ai-bridge"
	openRouterAppTitle   = "AI bridge for Beeper"
)

func openRouterHeaders() map[string]string {
	return map[string]string{
		"HTTP-Referer": openRouterAppReferer,
		"X-Title":      openRouterAppTitle,
	}
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
}

// queuePendingMessage adds a message to the pending queue for later processing
func (oc *AIClient) queuePendingMessage(roomID id.RoomID, msg pendingMessage) {
	oc.pendingMessagesMu.Lock()
	defer oc.pendingMessagesMu.Unlock()
	oc.pendingMessages[roomID] = append(oc.pendingMessages[roomID], msg)
	oc.log.Debug().
		Str("room_id", roomID.String()).
		Int("queue_length", len(oc.pendingMessages[roomID])).
		Msg("Message queued for later processing")
}

// popNextPending removes and returns the next pending message for a room, or nil if none
func (oc *AIClient) popNextPending(roomID id.RoomID) *pendingMessage {
	oc.pendingMessagesMu.Lock()
	defer oc.pendingMessagesMu.Unlock()
	queue := oc.pendingMessages[roomID]
	if len(queue) == 0 {
		return nil
	}
	msg := queue[0]
	oc.pendingMessages[roomID] = queue[1:]
	if len(oc.pendingMessages[roomID]) == 0 {
		delete(oc.pendingMessages, roomID)
	}
	return &msg
}

// dispatchOrQueue handles the common room acquisition pattern for message processing.
// If the room is available, it dispatches the completion immediately and returns Pending=true
// so message status can be flipped to SUCCESS on first response bytes.
// If the room is busy, it queues the message and sends a PENDING status.
func (oc *AIClient) dispatchOrQueue(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	userMessage *database.Message,
	pending pendingMessage,
	promptMessages []openai.ChatCompletionMessageParamUnion,
) (dbMessage *database.Message, isPending bool) {
	if oc.acquireRoom(portal.MXID) {
		// Save user message to database - we must do this ourselves since we return Pending=true.
		if userMessage != nil && evt != nil {
			userMessage.MXID = evt.ID
			if _, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userMessage.SenderID); err != nil {
				oc.log.Warn().Err(err).Msg("Failed to ensure user ghost before saving message")
			}
			if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, userMessage); err != nil {
				oc.log.Err(err).Msg("Failed to save user message to database")
			}
		}
		if !pending.PendingSent {
			oc.sendPendingStatus(ctx, portal, evt, "Processing...")
			pending.PendingSent = true
		}
		runCtx := withStatusEvents(ctx, pending.StatusEvents)
		metaSnapshot := clonePortalMetadata(meta)
		go func(metaSnapshot *PortalMetadata) {
			defer func() {
				// Remove ack reaction after response is complete (if configured)
				if metaSnapshot != nil && metaSnapshot.AckReactionRemoveAfter && evt != nil {
					oc.removeAckReaction(oc.backgroundContext(ctx), portal, evt.ID)
				}
				oc.releaseRoom(portal.MXID)
				oc.processNextPending(oc.backgroundContext(ctx), portal.MXID)
			}()
			oc.dispatchCompletionInternal(runCtx, evt, portal, metaSnapshot, promptMessages)
		}(metaSnapshot)
		oc.notifySessionMemoryChange(ctx, portal, meta, false)
		return userMessage, true
	}

	// Room busy - save message ourselves and queue for later
	if userMessage != nil {
		userMessage.MXID = evt.ID
		if _, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userMessage.SenderID); err != nil {
			oc.log.Warn().Err(err).Msg("Failed to ensure user ghost before saving queued message")
		}
		if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, userMessage); err != nil {
			oc.log.Err(err).Msg("Failed to save queued message to database")
		}
	}

	pending.PendingSent = true
	oc.queuePendingMessage(portal.MXID, pending)
	oc.sendPendingStatus(ctx, portal, evt, "Waiting for previous response")
	oc.notifySessionMemoryChange(ctx, portal, meta, false)
	return userMessage, true
}

// dispatchOrQueueWithStatus is like dispatchOrQueue but does not return a DB message.
// Used for regenerate/edit operations.
func (oc *AIClient) dispatchOrQueueWithStatus(
	ctx context.Context,
	evt *event.Event,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	pending pendingMessage,
	promptMessages []openai.ChatCompletionMessageParamUnion,
) {
	if oc.acquireRoom(portal.MXID) {
		runCtx := withStatusEvents(ctx, pending.StatusEvents)
		metaSnapshot := clonePortalMetadata(meta)
		go func(metaSnapshot *PortalMetadata) {
			defer func() {
				oc.releaseRoom(portal.MXID)
				oc.processNextPending(oc.backgroundContext(ctx), portal.MXID)
			}()
			oc.dispatchCompletionInternal(runCtx, evt, portal, metaSnapshot, promptMessages)
		}(metaSnapshot)
		return
	}

	oc.queuePendingMessage(portal.MXID, pending)
	oc.sendPendingStatus(ctx, portal, evt, "Waiting for previous response")
}

// processNextPending processes the next pending message for a room if one exists
func (oc *AIClient) processNextPending(ctx context.Context, roomID id.RoomID) {
	pending := oc.popNextPending(roomID)
	if pending == nil {
		return
	}

	oc.log.Debug().
		Str("room_id", roomID.String()).
		Str("type", string(pending.Type)).
		Msg("Processing next pending message")

	// Re-acquire the room lock and process
	if !oc.acquireRoom(roomID) {
		// Room somehow got busy again, re-queue the message
		oc.queuePendingMessage(roomID, *pending)
		return
	}

	// Build prompt NOW with fresh history (includes previous AI responses)
	var promptMessages []openai.ChatCompletionMessageParamUnion
	var err error
	eventID := id.EventID("")
	if pending.Event != nil {
		eventID = pending.Event.ID
	}

	metaSnapshot := clonePortalMetadata(pending.Meta)
	runCtx := withStatusEvents(ctx, pending.StatusEvents)

	switch pending.Type {
	case pendingTypeText:
		promptMessages, err = oc.buildPrompt(runCtx, pending.Portal, metaSnapshot, pending.MessageBody, eventID)
	case pendingTypeImage, pendingTypePDF, pendingTypeAudio, pendingTypeVideo:
		promptMessages, err = oc.buildPromptWithMedia(runCtx, pending.Portal, metaSnapshot, pending.MessageBody, pending.MediaURL, pending.MimeType, pending.EncryptedFile, pending.Type, eventID)
	case pendingTypeRegenerate:
		promptMessages, err = oc.buildPromptForRegenerate(runCtx, pending.Portal, metaSnapshot, pending.MessageBody, pending.SourceEventID)
	case pendingTypeEditRegenerate:
		promptMessages, err = oc.buildPromptUpToMessage(runCtx, pending.Portal, metaSnapshot, pending.TargetMsgID, pending.MessageBody)
	default:
		err = fmt.Errorf("unknown pending message type: %s", pending.Type)
	}

	if err != nil {
		oc.log.Err(err).Str("type", string(pending.Type)).Msg("Failed to build prompt for pending message")
		oc.notifyMatrixSendFailure(runCtx, pending.Portal, pending.Event, err)
		if metaSnapshot != nil && metaSnapshot.AckReactionRemoveAfter && pending.Event != nil {
			oc.removeAckReaction(runCtx, pending.Portal, pending.Event.ID)
		}
		oc.releaseRoom(roomID)
		oc.processNextPending(oc.backgroundContext(ctx), roomID)
		return
	}

	// Process in background, will release room when done
	go func() {
		defer func() {
			// Remove ack reaction after response is complete (if configured)
			if metaSnapshot != nil && metaSnapshot.AckReactionRemoveAfter && pending.Event != nil {
				oc.removeAckReaction(oc.backgroundContext(ctx), pending.Portal, pending.Event.ID)
			}
			oc.releaseRoom(roomID)
			// Check for more pending messages
			oc.processNextPending(oc.backgroundContext(ctx), roomID)
		}()
		oc.dispatchCompletionInternal(runCtx, pending.Event, pending.Portal, metaSnapshot, promptMessages)
	}()
}

func (oc *AIClient) Connect(ctx context.Context) {
	// Trust the token - auth errors will be caught during actual API usage
	// OpenRouter and Beeper provider don't support the GET /v1/models/{model} endpoint
	oc.loggedIn.Store(true)
	oc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateConnected,
		Message:    "Connected",
	})

	if oc.heartbeatRunner != nil {
		oc.heartbeatRunner.Start()
	}
	if oc.cronService != nil {
		if err := oc.cronService.Start(); err != nil {
			oc.log.Warn().Err(err).Msg("cron: failed to start scheduler")
		}
	}
}

func (oc *AIClient) Disconnect() {
	// Flush pending debounced messages before disconnect (bridgev2 pattern)
	if oc.inboundDebouncer != nil {
		oc.log.Info().Msg("Flushing pending debounced messages on disconnect")
		oc.inboundDebouncer.FlushAll()
	}
	oc.loggedIn.Store(false)

	if oc.cronService != nil {
		oc.cronService.Stop()
	}
	if oc.heartbeatRunner != nil {
		oc.heartbeatRunner.Stop()
	}
}

func (oc *AIClient) IsLoggedIn() bool {
	return oc.loggedIn.Load()
}

func (oc *AIClient) LogoutRemote(ctx context.Context) {
	oc.Disconnect()
	oc.UserLogin.BridgeState.Send(status.BridgeState{
		StateEvent: status.StateLoggedOut,
		Message:    "Disconnected by user",
	})
}

func (oc *AIClient) IsThisUser(ctx context.Context, userID networkid.UserID) bool {
	return userID == humanUserID(oc.UserLogin.ID)
}

func (oc *AIClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	meta := portalMeta(portal)
	title := meta.Title
	if title == "" {
		if portal.Name != "" {
			title = portal.Name
		} else {
			title = "AI Chat"
		}
	}
	// Use actual portal.Topic, not SystemPrompt (they are separate concepts)
	return &bridgev2.ChatInfo{
		Name:  ptr.Ptr(title),
		Topic: ptrIfNotEmpty(portal.Topic),
	}, nil
}

func (oc *AIClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	ghostID := string(ghost.ID)

	// Parse agent from ghost ID (format: "agent-{id}")
	if agentID, ok := parseAgentFromGhostID(ghostID); ok {
		store := NewAgentStoreAdapter(oc)
		agent, err := store.GetAgentByID(ctx, agentID)
		displayName := "Unknown Agent"
		modelID := oc.agentModelOverride(agentID)
		if err == nil && agent != nil {
			displayName = oc.resolveAgentDisplayName(ctx, agent)
			if displayName == "" {
				displayName = agent.Name
			}
			if displayName == "" {
				displayName = agent.ID
			}
			if modelID == "" && agent.Model.Primary != "" {
				modelID = ResolveAlias(agent.Model.Primary)
			}
		}
		identifiers := []string{agentID}
		if modelID != "" {
			identifiers = append(identifiers, modelContactIdentifiers(modelID, oc.findModelInfo(modelID))...)
		}
		return &bridgev2.UserInfo{
			Name:         ptr.Ptr(displayName),
			IsBot:        ptr.Ptr(true),
			Identifiers:  uniqueStrings(identifiers),
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

	// Always recompute effective room capabilities to ensure they're up-to-date
	// (includes image-understanding union for agent rooms)
	modelCaps := oc.getRoomCapabilities(ctx, meta)
	allowTextFiles := oc.canUseMediaUnderstanding(meta)

	// Clone base capabilities
	caps := ptr.Clone(aiBaseCaps)

	// Build dynamic capability ID from modalities
	caps.ID = buildCapabilityID(modelCaps)

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
	if modelCaps.SupportsPDF || oc.isOpenRouterProvider() {
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

// effectiveModel returns the full prefixed model ID (e.g., "openai/gpt-5.2")
// Priority: Room → Agent → User → Provider → Global
// Exception: Boss agent rooms always use the Boss agent's model (no overrides)
func (oc *AIClient) effectiveModel(meta *PortalMetadata) string {
	// Check if an agent is assigned
	if meta != nil {
		agentID := resolveAgentID(meta)
		if agentID != "" {
			// Load the agent to get its model
			store := NewAgentStoreAdapter(oc)
			agent, err := store.GetAgentByID(context.Background(), agentID)
			if err == nil && agent != nil {
				// Boss agent rooms always use the Boss model - no overrides allowed
				if agents.IsBossAgent(agentID) && agent.Model.Primary != "" {
					return ResolveAlias(agent.Model.Primary)
				}
				// For other agents, room override takes priority, then agent model
				if meta.Model != "" {
					return ResolveAlias(meta.Model)
				}
				if override := oc.agentModelOverride(agentID); override != "" {
					return ResolveAlias(override)
				}
				if agent.Model.Primary != "" {
					return ResolveAlias(agent.Model.Primary)
				}
			}
		}
	}

	// Room-level model override (for rooms without an agent)
	if meta != nil && meta.Model != "" {
		return ResolveAlias(meta.Model)
	}

	// User-level default
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta.Defaults != nil && loginMeta.Defaults.Model != "" {
		return ResolveAlias(loginMeta.Defaults.Model)
	}

	// Provider default from config
	return oc.defaultModelForProvider()
}

func (oc *AIClient) agentModelOverride(agentID string) string {
	if agentID == "" || oc.UserLogin == nil {
		return ""
	}
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta == nil || loginMeta.AgentModelOverrides == nil {
		return ""
	}
	return strings.TrimSpace(loginMeta.AgentModelOverrides[agentID])
}

//lint:ignore U1000 Staged for future agent model override wiring.
func (oc *AIClient) setAgentModelOverride(ctx context.Context, agentID, modelID string) error {
	if agentID == "" || oc.UserLogin == nil {
		return fmt.Errorf("missing agent ID")
	}
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta.AgentModelOverrides == nil {
		loginMeta.AgentModelOverrides = make(map[string]string)
	}
	if modelID == "" {
		delete(loginMeta.AgentModelOverrides, agentID)
	} else {
		loginMeta.AgentModelOverrides[agentID] = modelID
	}
	return oc.UserLogin.Save(ctx)
}

// effectiveModelForAPI returns the actual model name to send to the API
// For OpenRouter/Beeper, returns the full model ID (e.g., "openai/gpt-5.2")
// For direct providers, strips the prefix (e.g., "openai/gpt-5.2" → "gpt-5.2")
func (oc *AIClient) effectiveModelForAPI(meta *PortalMetadata) string {
	modelID := oc.effectiveModel(meta)

	// OpenRouter and Beeper route through a gateway that expects the full model ID
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta.Provider == ProviderOpenRouter || loginMeta.Provider == ProviderBeeper {
		return modelID
	}

	// Direct OpenAI provider needs the prefix stripped
	_, actualModel := ParseModelPrefix(modelID)
	return actualModel
}

// modelIDForAPI converts a full model ID to the provider-specific API model name.
// For OpenRouter/Beeper, returns the full model ID.
// For direct providers, strips the prefix (e.g., "openai/gpt-5.2" → "gpt-5.2").
func (oc *AIClient) modelIDForAPI(modelID string) string {
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta.Provider == ProviderOpenRouter || loginMeta.Provider == ProviderBeeper {
		return modelID
	}
	_, actualModel := ParseModelPrefix(modelID)
	return actualModel
}

// defaultModelForProvider returns the configured default model for this login's provider
func (oc *AIClient) defaultModelForProvider() string {
	loginMeta := loginMetadata(oc.UserLogin)
	providers := oc.connector.Config.Providers

	switch loginMeta.Provider {
	case ProviderOpenAI:
		if providers.OpenAI.DefaultModel != "" {
			return providers.OpenAI.DefaultModel
		}
		return DefaultModelOpenAI
	case ProviderOpenRouter:
		if providers.OpenRouter.DefaultModel != "" {
			return providers.OpenRouter.DefaultModel
		}
		return DefaultModelOpenRouter
	case ProviderBeeper:
		if providers.Beeper.DefaultModel != "" {
			return providers.Beeper.DefaultModel
		}
		return DefaultModelBeeper
	default:
		return DefaultModelOpenRouter
	}
}

// effectivePrompt returns the system prompt to use
// Priority: Room ? User ? Bridge Config
func (oc *AIClient) effectivePrompt(meta *PortalMetadata) string {
	// Room-level override takes priority
	var base string
	if meta != nil && meta.SystemPrompt != "" {
		base = meta.SystemPrompt
	} else {
		loginMeta := loginMetadata(oc.UserLogin)
		if loginMeta.Defaults != nil && loginMeta.Defaults.SystemPrompt != "" {
			base = loginMeta.Defaults.SystemPrompt
		} else {
			base = oc.connector.Config.DefaultSystemPrompt
		}
	}
	gravatarContext := oc.gravatarContext()
	if gravatarContext == "" {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return gravatarContext
	}
	return fmt.Sprintf("%s\n\n%s", base, gravatarContext)
}

// getLinkPreviewConfig returns the link preview configuration, with defaults filled in.
func (oc *AIClient) getLinkPreviewConfig() LinkPreviewConfig {
	config := DefaultLinkPreviewConfig()

	if oc.connector.Config.LinkPreviews != nil {
		cfg := oc.connector.Config.LinkPreviews
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

// effectiveAgentPrompt returns the system prompt for the agent assigned to the room.
// This uses BuildSystemPrompt to generate a full prompt with room context when an agent is configured.
// Returns empty string if no agent is configured.
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
		oc.log.Warn().Err(err).Str("agent", agentID).Msg("Failed to load agent for prompt")
		return ""
	}

	timezone, _ := oc.resolveUserTimezone()

	workspaceDir := resolvePromptWorkspaceDir()
	extraParts := []string{}
	if strings.TrimSpace(agent.SystemPrompt) != "" {
		extraParts = append(extraParts, strings.TrimSpace(agent.SystemPrompt))
	}
	if meta != nil && strings.TrimSpace(meta.SystemPrompt) != "" {
		extraParts = append(extraParts, strings.TrimSpace(meta.SystemPrompt))
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
	if oc.connector != nil && oc.connector.Config.Memory != nil {
		params.MemoryCitations = strings.TrimSpace(oc.connector.Config.Memory.Citations)
	}
	params.UserIdentitySupplement = oc.gravatarContext()
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

	// Build capabilities list from metadata
	var caps []string
	if meta.Capabilities.SupportsVision {
		caps = append(caps, "vision")
	}
	if meta.Capabilities.SupportsToolCalling {
		caps = append(caps, "tools")
	}
	if meta.Capabilities.SupportsReasoning {
		caps = append(caps, "reasoning")
	}
	if meta.Capabilities.SupportsAudio {
		caps = append(caps, "audio")
	}
	if meta.Capabilities.SupportsVideo {
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
		RepoRoot:     resolvePromptRepoRoot(workspaceDir),
	}

	// Reaction guidance - default to minimal for group chats
	if portal != nil && oc.isGroupChat(ctx, portal) {
		params.ReactionGuidance = &agents.ReactionGuidance{
			Level:   "minimal",
			Channel: "matrix",
		}
	}

	// Reasoning hints and level
	params.ReasoningTagHint = meta.Capabilities.SupportsReasoning && meta.EmitThinking
	params.ReasoningLevel = resolvePromptReasoningLevel(meta, oc.effectiveReasoningEffort(meta))

	// Default thinking level (OpenClaw prompt expects this value)
	params.DefaultThinkLevel = "off"

	return agents.BuildSystemPrompt(params)
}

// effectiveTemperature returns the temperature to use
// Priority: Room → User → Default (0.4)
func (oc *AIClient) effectiveTemperature(meta *PortalMetadata) float64 {
	if meta != nil && meta.Temperature > 0 {
		return meta.Temperature
	}
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta.Defaults != nil && loginMeta.Defaults.Temperature != nil {
		return *loginMeta.Defaults.Temperature
	}
	return defaultTemperature
}

// effectiveReasoningEffort returns the reasoning effort to use
// Priority: Room ? User ? "" (none)
func (oc *AIClient) effectiveReasoningEffort(meta *PortalMetadata) string {
	if meta != nil && !meta.Capabilities.SupportsReasoning {
		return ""
	}
	if meta != nil && meta.ReasoningEffort != "" {
		return meta.ReasoningEffort
	}
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta.Defaults != nil && loginMeta.Defaults.ReasoningEffort != "" {
		return loginMeta.Defaults.ReasoningEffort
	}
	if meta != nil && meta.Capabilities.SupportsReasoning {
		return defaultReasoningEffort
	}
	return ""
}

func (oc *AIClient) historyLimit(meta *PortalMetadata) int {
	if meta != nil && meta.MaxContextMessages > 0 {
		return meta.MaxContextMessages
	}
	return defaultMaxContextMessages
}

func (oc *AIClient) effectiveMaxTokens(meta *PortalMetadata) int {
	if meta != nil && meta.MaxCompletionTokens > 0 {
		return meta.MaxCompletionTokens
	}
	return defaultMaxTokens
}

// isOpenRouterProvider checks if the current provider is OpenRouter or Beeper (which uses OpenRouter)
func (oc *AIClient) isOpenRouterProvider() bool {
	loginMeta := loginMetadata(oc.UserLogin)
	return loginMeta.Provider == ProviderOpenRouter || loginMeta.Provider == ProviderBeeper
}

// isGroupChat determines if the portal is a group chat based on member count.
// Returns true when there are more than 2 members (user + bot + others = group).
func (oc *AIClient) isGroupChat(ctx context.Context, portal *bridgev2.Portal) bool {
	if portal == nil || portal.MXID == "" {
		return false
	}

	// Get member count
	matrixConn := oc.UserLogin.Bridge.Matrix
	if matrixConn == nil {
		return false
	}
	members, err := matrixConn.GetMembers(ctx, portal.MXID)
	if err != nil {
		oc.log.Debug().Err(err).Msg("Failed to get joined members for group chat detection")
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
	case ProviderBeeper:
		if engine := oc.connector.Config.Providers.Beeper.DefaultPDFEngine; engine != "" {
			return engine
		}
	case ProviderOpenRouter:
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

	// First check local model cache
	models, err := oc.listAvailableModels(ctx, false)
	if err == nil {
		for _, model := range models {
			if model.ID == modelID {
				return true, nil
			}
		}
	}
	return false, nil
}

// resolveModelID tries to normalize a user-provided model to a known model ID.
// It accepts exact IDs, aliases, display names, and suffix-only IDs (e.g. "gpt-4o-mini").
func (oc *AIClient) resolveModelID(ctx context.Context, modelID string) (string, bool, error) {
	normalized := strings.TrimSpace(modelID)
	if normalized == "" {
		return "", true, nil
	}

	normalized = ResolveAlias(normalized)

	models, err := oc.listAvailableModels(ctx, false)
	if err == nil {
		for _, model := range models {
			if model.ID == normalized {
				return model.ID, true, nil
			}
		}

		lower := strings.ToLower(normalized)
		for _, model := range models {
			if strings.ToLower(model.ID) == lower {
				return model.ID, true, nil
			}
		}

		for _, model := range models {
			if strings.EqualFold(model.Name, normalized) {
				return model.ID, true, nil
			}
		}

		if strings.Contains(normalized, "/") {
			parts := strings.SplitN(normalized, "/", 2)
			providerPart := parts[0]
			rest := parts[1]
			if providerPart != "" && rest != "" {
				for _, model := range models {
					modelProvider := model.Provider
					if modelProvider == "" {
						if backend, _ := ParseModelPrefix(model.ID); backend != "" {
							modelProvider = string(backend)
						}
					}
					if modelProvider == "" || !strings.EqualFold(modelProvider, providerPart) {
						continue
					}
					if strings.EqualFold(model.ID, rest) ||
						strings.EqualFold(model.Name, rest) ||
						strings.HasSuffix(strings.ToLower(model.ID), "/"+strings.ToLower(rest)) {
						return model.ID, true, nil
					}
				}
			}
		}

		if !strings.Contains(normalized, "/") {
			var match string
			for _, model := range models {
				if strings.HasSuffix(model.ID, "/"+normalized) {
					if match != "" && match != model.ID {
						return "", false, nil
					}
					match = model.ID
				}
			}
			if match != "" {
				return match, true, nil
			}
		}
	}

	return "", false, nil
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

	oc.log.Debug().Msg("Loading model catalog from VFS")
	if _, err := oc.ensureModelCatalogVFS(ctx); err != nil {
		oc.log.Warn().Err(err).Msg("Failed to seed model catalog")
	}
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
		oc.log.Warn().Err(err).Msg("Failed to save model cache")
	}

	oc.log.Info().Int("count", len(allModels)).Msg("Cached available models")
	return allModels, nil
}

// findModelInfo looks up ModelInfo from the user's model cache by ID
func (oc *AIClient) findModelInfo(modelID string) *ModelInfo {
	meta := loginMetadata(oc.UserLogin)
	if meta.ModelCache == nil {
		goto catalogFallback
	}
	for i := range meta.ModelCache.Models {
		if meta.ModelCache.Models[i].ID == modelID {
			return &meta.ModelCache.Models[i]
		}
	}
catalogFallback:
	return oc.findModelInfoInCatalog(modelID)
}

// buildBasePrompt builds the system prompt and history portion of a prompt.
// This is the common pattern used by buildPrompt and buildPromptWithImage.
func (oc *AIClient) buildBasePrompt(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
) ([]openai.ChatCompletionMessageParamUnion, error) {
	var prompt []openai.ChatCompletionMessageParamUnion
	prompt = maybePrependSessionGreeting(ctx, portal, meta, prompt, oc.log)

	// Add system prompt - agent prompt takes priority, then room override, then config default
	systemPrompt := oc.effectiveAgentPrompt(ctx, portal, meta)
	if systemPrompt == "" {
		systemPrompt = oc.effectivePrompt(meta)
	}
	if systemPrompt != "" {
		prompt = append(prompt, openai.SystemMessage(systemPrompt))
	}

	// Add history
	historyLimit := oc.historyLimit(meta)
	if historyLimit > 0 {
		history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, historyLimit)
		if err != nil {
			return nil, fmt.Errorf("failed to load prompt history: %w", err)
		}
		for i := len(history) - 1; i >= 0; i-- {
			msgMeta := messageMeta(history[i])
			if !shouldIncludeInHistory(msgMeta) {
				continue
			}
			// Include message ID so the AI can reference specific messages for reactions/replies.
			// Format: message body + "\n[message_id: $eventId]" (matches clawdbot pattern).
			body := msgMeta.Body
			if history[i].MXID != "" {
				body = appendMessageIDHint(msgMeta.Body, history[i].MXID)
			}
			switch msgMeta.Role {
			case "assistant":
				prompt = append(prompt, openai.AssistantMessage(body))
			default:
				prompt = append(prompt, openai.UserMessage(body))
			}
		}
	}

	return prompt, nil
}

// buildPrompt builds a prompt with the latest user message
func (oc *AIClient) buildPrompt(ctx context.Context, portal *bridgev2.Portal, meta *PortalMetadata, latest string, eventID id.EventID) ([]openai.ChatCompletionMessageParamUnion, error) {
	return oc.buildPromptWithLinkContext(ctx, portal, meta, latest, nil, eventID)
}

// buildPromptWithLinkContext builds a prompt with the latest user message and optional link context.
// If rawEventContent is provided, it will extract existing link previews from it.
// URLs in the message will be auto-fetched if no preview exists.
func (oc *AIClient) buildPromptWithLinkContext(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	latest string,
	rawEventContent map[string]any,
	eventID id.EventID,
) ([]openai.ChatCompletionMessageParamUnion, error) {
	prompt, err := oc.buildBasePrompt(ctx, portal, meta)
	if err != nil {
		return nil, err
	}
	prompt = oc.injectMemoryContext(ctx, portal, meta, prompt)

	// Build final message with link context
	finalMessage := latest
	linkContext := oc.buildLinkContext(ctx, latest, rawEventContent)
	if linkContext != "" {
		finalMessage = latest + linkContext
	}

	// Include reaction feedback from users (like OpenClaw's system events)
	// This lets the AI know when users react to its messages
	if portal != nil && portal.MXID != "" {
		reactionFeedback := DrainReactionFeedback(portal.MXID)
		if len(reactionFeedback) > 0 {
			feedbackText := FormatReactionFeedback(reactionFeedback)
			if feedbackText != "" {
				// Prepend feedback to user message so AI sees recent reactions
				finalMessage = feedbackText + "\n" + finalMessage
			}
		}
	}

	finalMessage = appendMessageIDHint(finalMessage, eventID)
	prompt = append(prompt, openai.UserMessage(finalMessage))
	return prompt, nil
}

// buildLinkContext extracts URLs from the message, fetches previews, and returns formatted context.
func (oc *AIClient) buildLinkContext(ctx context.Context, message string, rawEventContent map[string]any) string {
	config := oc.getLinkPreviewConfig()
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

// buildPromptWithMedia builds a prompt with media content (image, PDF, audio, or video)
func (oc *AIClient) buildPromptWithMedia(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	caption string,
	mediaURL string,
	mimeType string,
	encryptedFile *event.EncryptedFileInfo,
	mediaType pendingMessageType,
	eventID id.EventID,
) ([]openai.ChatCompletionMessageParamUnion, error) {
	prompt, err := oc.buildBasePrompt(ctx, portal, meta)
	if err != nil {
		return nil, err
	}
	prompt = oc.injectMemoryContext(ctx, portal, meta, prompt)

	captionWithID := appendMessageIDHint(caption, eventID)
	textContent := openai.ChatCompletionContentPartUnionParam{
		OfText: &openai.ChatCompletionContentPartTextParam{
			Text: captionWithID,
		},
	}

	var mediaContent openai.ChatCompletionContentPartUnionParam

	switch mediaType {
	case pendingTypeImage:
		// Always download+base64 for images (consistent across cloud/self-hosted)
		b64Data, actualMimeType, err := oc.downloadMediaBase64(ctx, mediaURL, encryptedFile, 20, mimeType) // 20MB limit for images
		if err != nil {
			return nil, fmt.Errorf("failed to download image: %w", err)
		}
		dataURL := buildDataURL(actualMimeType, b64Data)
		mediaContent = openai.ChatCompletionContentPartUnionParam{
			OfImageURL: &openai.ChatCompletionContentPartImageParam{
				ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
					URL:    dataURL,
					Detail: "auto",
				},
			},
		}

	case pendingTypePDF:
		// Download and base64 encode the PDF (always need to encode, encrypted or not)
		b64Data, actualMimeType, err := oc.downloadMediaBase64(ctx, mediaURL, encryptedFile, 50, mimeType) // 50MB limit
		if err != nil {
			return nil, fmt.Errorf("failed to download PDF: %w", err)
		}
		if actualMimeType == "" {
			actualMimeType = "application/pdf"
		}
		dataURL := buildDataURL(actualMimeType, b64Data)
		mediaContent = openai.ChatCompletionContentPartUnionParam{
			OfFile: &openai.ChatCompletionContentPartFileParam{
				File: openai.ChatCompletionContentPartFileFileParam{
					FileData: openai.String(dataURL),
				},
			},
		}

	case pendingTypeAudio:
		// Download and base64 encode the audio (always need to encode)
		b64Data, actualMimeType, err := oc.downloadMediaBase64(ctx, mediaURL, encryptedFile, 25, mimeType) // 25MB limit
		if err != nil {
			return nil, fmt.Errorf("failed to download audio: %w", err)
		}
		audioFormat := getAudioFormat(actualMimeType)
		mediaContent = openai.ChatCompletionContentPartUnionParam{
			OfInputAudio: &openai.ChatCompletionContentPartInputAudioParam{
				InputAudio: openai.ChatCompletionContentPartInputAudioInputAudioParam{
					Data:   b64Data,
					Format: audioFormat,
				},
			},
		}

	case pendingTypeVideo:
		// Always download+base64 for video (consistent across cloud/self-hosted)
		b64Data, actualMimeType, err := oc.downloadMediaBase64(ctx, mediaURL, encryptedFile, 100, mimeType) // 100MB limit for video
		if err != nil {
			return nil, fmt.Errorf("failed to download video: %w", err)
		}
		dataURL := buildDataURL(actualMimeType, b64Data)
		if oc.isOpenRouterProvider() {
			videoPart := param.Override[openai.ChatCompletionContentPartUnionParam](map[string]any{
				"type": "video_url",
				"video_url": map[string]any{
					"url": dataURL,
				},
			})
			userMsg := openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
							textContent,
							videoPart,
						},
					},
				},
			}
			prompt = append(prompt, userMsg)
			return prompt, nil
		}
		videoPrompt := fmt.Sprintf("%s\n\nVideo data URL: %s", caption, dataURL)
		videoPrompt = appendMessageIDHint(videoPrompt, eventID)
		userMsg := openai.ChatCompletionMessageParamUnion{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: openai.String(videoPrompt),
				},
			},
		}
		prompt = append(prompt, userMsg)
		return prompt, nil

	default:
		return nil, fmt.Errorf("unsupported media type: %s", mediaType)
	}

	// Create user message with both text and media content
	userMsg := openai.ChatCompletionMessageParamUnion{
		OfUser: &openai.ChatCompletionUserMessageParam{
			Content: openai.ChatCompletionUserMessageParamContentUnion{
				OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
					textContent,
					mediaContent,
				},
			},
		},
	}

	prompt = append(prompt, userMsg)
	return prompt, nil
}

// buildPromptUpToMessage builds a prompt including messages up to and including the specified message
func (oc *AIClient) buildPromptUpToMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	targetMessageID networkid.MessageID,
	newBody string,
) ([]openai.ChatCompletionMessageParamUnion, error) {
	var prompt []openai.ChatCompletionMessageParamUnion

	// Add system prompt
	systemPrompt := oc.effectivePrompt(meta)
	if systemPrompt != "" {
		prompt = append(prompt, openai.SystemMessage(systemPrompt))
	}

	// Get history
	historyLimit := oc.historyLimit(meta)
	if historyLimit > 0 {
		history, err := oc.UserLogin.Bridge.DB.Message.GetLastNInPortal(ctx, portal.PortalKey, historyLimit)
		if err != nil {
			return nil, fmt.Errorf("failed to load prompt history: %w", err)
		}

		// Add messages up to the target message, replacing the target with newBody
		for i := len(history) - 1; i >= 0; i-- {
			msg := history[i]
			msgMeta := messageMeta(msg)

			// Stop after adding the target message
			if msg.ID == targetMessageID {
				// Use the new body for the edited message
				body := newBody
				if msg.MXID != "" {
					body = appendMessageIDHint(newBody, msg.MXID)
				}
				prompt = append(prompt, openai.UserMessage(body))
				break
			}

			// Skip commands and non-conversation messages
			if !shouldIncludeInHistory(msgMeta) {
				continue
			}

			// Skip assistant messages that came after the target (we're going backwards)
			body := msgMeta.Body
			if msg.MXID != "" {
				body = appendMessageIDHint(msgMeta.Body, msg.MXID)
			}
			switch msgMeta.Role {
			case "assistant":
				prompt = append(prompt, openai.AssistantMessage(body))
			default:
				prompt = append(prompt, openai.UserMessage(body))
			}
		}
	} else {
		// No history, just add the new message
		prompt = append(prompt, openai.UserMessage(newBody))
	}

	return prompt, nil
}

// downloadAndEncodeMedia downloads media from Matrix and returns base64-encoded data
// If encryptedFile is provided, decrypts the media using AES-CTR
// maxSizeMB limits the download size (0 = no limit)
// Returns (base64Data, mimeType, error)
func (oc *AIClient) downloadAndEncodeMedia(ctx context.Context, mxcURL string, encryptedFile *event.EncryptedFileInfo, maxSizeMB int) (string, string, error) {
	// For encrypted media, use the URL from the encrypted file info
	downloadURL := mxcURL
	if encryptedFile != nil {
		downloadURL = string(encryptedFile.URL)
	}

	// Handle local file URLs/paths (common in local rooms)
	if strings.HasPrefix(downloadURL, "file://") || strings.HasPrefix(downloadURL, "/") {
		path := downloadURL
		if strings.HasPrefix(path, "file://") {
			path = strings.TrimPrefix(path, "file://")
			if unescaped, err := url.PathUnescape(path); err == nil {
				path = unescaped
			}
		}

		info, err := os.Stat(path)
		if err != nil {
			return "", "", fmt.Errorf("failed to stat local file: %w", err)
		}
		if maxSizeMB > 0 {
			maxBytes := int64(maxSizeMB * 1024 * 1024)
			if info.Size() > maxBytes {
				return "", "", fmt.Errorf("media too large: %d bytes (max %d MB)", info.Size(), maxSizeMB)
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return "", "", fmt.Errorf("failed to read local file: %w", err)
		}
		if encryptedFile != nil {
			if err := encryptedFile.DecryptInPlace(data); err != nil {
				return "", "", fmt.Errorf("failed to decrypt media: %w", err)
			}
		}

		mimeType := mime.TypeByExtension(filepath.Ext(path))
		if mimeType == "" {
			mimeType = http.DetectContentType(data)
		}
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}

		b64Data := base64.StdEncoding.EncodeToString(data)
		return b64Data, mimeType, nil
	}

	if strings.HasPrefix(downloadURL, "mxc://") {
		if oc.UserLogin == nil || oc.UserLogin.Bridge == nil || oc.UserLogin.Bridge.Bot == nil {
			return "", "", fmt.Errorf("matrix API unavailable for MXC media download")
		}
		data, err := oc.UserLogin.Bridge.Bot.DownloadMedia(ctx, id.ContentURIString(downloadURL), encryptedFile)
		if err != nil {
			return "", "", fmt.Errorf("failed to download media via Matrix API: %w", err)
		}
		if maxSizeMB > 0 {
			maxBytes := int64(maxSizeMB * 1024 * 1024)
			if int64(len(data)) > maxBytes {
				return "", "", fmt.Errorf("media too large (max %d MB)", maxSizeMB)
			}
		}
		mimeType := http.DetectContentType(data)
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		b64Data := base64.StdEncoding.EncodeToString(data)
		return b64Data, mimeType, nil
	}

	httpURL := downloadURL

	// Create HTTP request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, httpURL, nil)
	if err != nil {
		return "", "", fmt.Errorf("failed to create request: %w", err)
	}

	// Use a client with timeout
	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to download media: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Check content length if available
	if maxSizeMB > 0 && resp.ContentLength > 0 {
		maxBytes := int64(maxSizeMB * 1024 * 1024)
		if resp.ContentLength > maxBytes {
			return "", "", fmt.Errorf("media too large: %d bytes (max %d MB)", resp.ContentLength, maxSizeMB)
		}
	}

	// Read with size limit
	var reader io.Reader = resp.Body
	if maxSizeMB > 0 {
		maxBytes := int64(maxSizeMB * 1024 * 1024)
		reader = io.LimitReader(resp.Body, maxBytes+1) // +1 to detect overflow
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return "", "", fmt.Errorf("failed to read media: %w", err)
	}

	// Check if we hit the size limit
	if maxSizeMB > 0 {
		maxBytes := int64(maxSizeMB * 1024 * 1024)
		if int64(len(data)) > maxBytes {
			return "", "", fmt.Errorf("media too large (max %d MB)", maxSizeMB)
		}
	}

	// Decrypt if encrypted (E2EE media)
	if encryptedFile != nil {
		if err := encryptedFile.DecryptInPlace(data); err != nil {
			return "", "", fmt.Errorf("failed to decrypt media: %w", err)
		}
	}

	// Get MIME type from response header
	mimeType := resp.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	// Base64 encode
	b64Data := base64.StdEncoding.EncodeToString(data)

	return b64Data, mimeType, nil
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
		oc.log.Debug().Str("model", modelID).Str("name", displayName).Msg("Updated ghost display name")
	}
}

// ensureAgentGhostDisplayName ensures the agent ghost has its display name set.
func (oc *AIClient) ensureAgentGhostDisplayName(ctx context.Context, agentID, modelID, agentName string) {
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, agentUserID(agentID))
	if err != nil || ghost == nil {
		return
	}
	displayName := oc.agentModelDisplayName(agentName, modelID)
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
			Identifiers: modelContactIdentifiers(modelID, oc.findModelInfo(modelID)),
			Avatar:      avatar,
		})
		oc.log.Debug().Str("agent", agentID).Str("model", modelID).Str("name", displayName).Msg("Updated agent ghost display name")
	}
}

// getModelIntent returns the Matrix intent for the current model or agent's ghost in the portal.
// If an agent is configured for the room, returns the agent ghost's intent.
// Otherwise, falls back to the model ghost's intent.
func (oc *AIClient) getModelIntent(ctx context.Context, portal *bridgev2.Portal) bridgev2.MatrixAPI {
	meta := portalMeta(portal)

	// Check if an agent is configured for this room
	agentID := resolveAgentID(meta)

	// Use agent ghost if an agent is configured
	if agentID != "" {
		modelID := oc.effectiveModel(meta)
		ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, agentUserID(agentID))
		if err == nil && ghost != nil {
			// Ensure the ghost has a display name set
			store := NewAgentStoreAdapter(oc)
			agent, _ := store.GetAgentByID(ctx, agentID)
			if agent != nil {
				agentName := oc.resolveAgentDisplayName(ctx, agent)
				oc.ensureAgentGhostDisplayName(ctx, agentID, modelID, agentName)
			}
			return ghost.Intent
		}
		oc.log.Warn().Err(err).Str("agent", agentID).Msg("Failed to get agent ghost, falling back to model")
	}

	// Fall back to model ghost
	modelID := oc.effectiveModel(meta)
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, modelUserID(modelID))
	if err != nil {
		oc.log.Warn().Err(err).Str("model", modelID).Msg("Failed to get model ghost")
		return nil
	}
	return ghost.Intent
}

// ensureModelInRoom ensures the current model's ghost is joined to the portal room.
// This should be called before any operations that require the model to be in the room
// (typing indicators, sending messages, etc.) to handle race conditions with model switching.
func (oc *AIClient) ensureModelInRoom(ctx context.Context, portal *bridgev2.Portal) error {
	if portal == nil || portal.MXID == "" {
		return fmt.Errorf("invalid portal")
	}
	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return fmt.Errorf("failed to get model intent")
	}
	return intent.EnsureJoined(ctx, portal.MXID)
}

func (oc *AIClient) backgroundContext(_ context.Context) context.Context {
	// Always prefer BackgroundCtx for long-running operations that outlive request context
	if oc.UserLogin != nil && oc.UserLogin.Bridge != nil && oc.UserLogin.Bridge.BackgroundCtx != nil {
		return oc.UserLogin.Bridge.BackgroundCtx
	}
	return context.Background()
}

func ptrIfNotEmpty(value string) *string {
	if value == "" {
		return nil
	}
	return ptr.Ptr(value)
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
		if info.SupportsReasoning {
			caps.SupportsReasoning = true
		}
		caps.SupportsToolCalling = info.SupportsToolCalling
	}

	return caps
}

// AgentState tracks the state of an active agent turn
type AgentState struct {
	AgentID     string
	TurnID      string
	Status      string // pending, thinking, generating, tool_use, completed, failed, cancelled
	StartedAt   time.Time
	Model       string
	ToolCalls   []string // Event IDs of tool calls
	ImageEvents []string // Event IDs of generated images
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

	last := entries[len(entries)-1]
	ctx := oc.backgroundContext(context.Background())

	// Combine message bodies if multiple
	combinedBody, count := CombineDebounceEntries(entries)
	if count > 1 {
		oc.log.Info().Int("count", count).Msg("Combined debounced messages")
	}

	extraStatusEvents := make([]*event.Event, 0, len(entries)-1)
	if len(entries) > 1 {
		for _, entry := range entries[:len(entries)-1] {
			if entry.Event != nil {
				extraStatusEvents = append(extraStatusEvents, entry.Event)
			}
		}
	}
	statusCtx := withStatusEvents(ctx, extraStatusEvents)

	// Build prompt with combined body
	promptMessages, err := oc.buildPromptWithLinkContext(statusCtx, last.Portal, last.Meta, combinedBody, nil, last.Event.ID)
	if err != nil {
		oc.log.Err(err).Msg("Failed to build prompt for debounced messages")
		oc.notifyMatrixSendFailure(statusCtx, last.Portal, last.Event, err)
		if last.Meta.AckReactionRemoveAfter && entries[0].AckEventID != "" {
			oc.removeAckReactionByID(statusCtx, last.Portal, entries[0].AckEventID)
		}
		return
	}

	// Create user message for database
	userMessage := &database.Message{
		ID:       networkid.MessageID(fmt.Sprintf("mx:%s", string(last.Event.ID))),
		MXID:     last.Event.ID,
		Room:     last.Portal.PortalKey,
		SenderID: humanUserID(oc.UserLogin.ID),
		Metadata: &MessageMetadata{
			Role: "user",
			Body: combinedBody,
		},
		Timestamp: time.Now(),
	}

	// Save user message to database - we must do this ourselves since we already
	// returned Pending: true to the bridge framework when debouncing started
	// Ensure ghost row exists to avoid foreign key violations.
	if _, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userMessage.SenderID); err != nil {
		oc.log.Warn().Err(err).Msg("Failed to ensure user ghost before saving debounced message")
	}
	if err := oc.UserLogin.Bridge.DB.Message.Insert(ctx, userMessage); err != nil {
		oc.log.Err(err).Msg("Failed to save debounced user message to database")
	}

	// Dispatch using existing flow (handles room lock + status)
	// Pass nil for userMessage since we already saved it above
	_, _ = oc.dispatchOrQueue(statusCtx, last.Event, last.Portal, last.Meta, nil,
		pendingMessage{
			Event:        last.Event,
			Portal:       last.Portal,
			Meta:         last.Meta,
			Type:         pendingTypeText,
			MessageBody:  combinedBody,
			StatusEvents: extraStatusEvents,
			PendingSent:  last.PendingSent,
		}, promptMessages)

	// Remove ack reaction from first entry if configured
	if last.Meta.AckReactionRemoveAfter && entries[0].AckEventID != "" {
		oc.removeAckReactionByID(statusCtx, last.Portal, entries[0].AckEventID)
	}
}

// removeAckReactionByID removes an ack reaction by its event ID.
func (oc *AIClient) removeAckReactionByID(ctx context.Context, portal *bridgev2.Portal, reactionEventID id.EventID) {
	if portal == nil || portal.MXID == "" || reactionEventID == "" {
		return
	}

	intent := oc.getModelIntent(ctx, portal)
	if intent == nil {
		return
	}

	// Redact the ack reaction
	_, err := intent.SendMessage(ctx, portal.MXID, event.EventRedaction, &event.Content{
		Parsed: &event.RedactionEventContent{
			Redacts: reactionEventID,
		},
	}, nil)
	if err != nil {
		oc.log.Warn().Err(err).
			Stringer("reaction_event", reactionEventID).
			Msg("Failed to remove ack reaction by ID")
	} else {
		oc.log.Debug().
			Stringer("reaction_event", reactionEventID).
			Msg("Removed ack reaction by ID")
	}
}
