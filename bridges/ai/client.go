package ai

import (
	"context"
	"errors"
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

	airuntime "github.com/beeper/agentremote/pkg/runtime"
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

// AIClient handles communication with AI providers
type AIClient struct {
	sdk.ClientBase
	UserLogin *bridgev2.UserLogin
	connector *OpenAIConnector
	api       openai.Client
	apiKey    string
	log       zerolog.Logger

	provider *OpenAIProvider

	chatLock      sync.Mutex
	loginStateMu  sync.Mutex
	loginState    *loginRuntimeState
	loginConfigMu sync.Mutex
	loginConfig   *aiLoginConfig

	// Pending message queue per room (for turn-based behavior)
	pendingQueues   map[id.RoomID]*pendingQueue
	pendingQueuesMu sync.Mutex

	// Active room runs and room occupancy (for admission, interrupt/steer, and tool-boundary steering).
	activeRoomRuns   map[id.RoomID]*roomRunState
	activeRoomRunsMu sync.Mutex

	// Pending group history buffers (mention-gated group context).
	groupHistoryBuffers map[id.RoomID]*groupHistoryBuffer
	groupHistoryMu      sync.Mutex

	// Message deduplication cache
	inboundDedupeCache *DedupeCache

	// Message debouncer for combining rapid messages
	inboundDebouncer *Debouncer

	// Typing indicator while messages are queued (per room)
	queueTypingMu sync.Mutex
	queueTyping   map[id.RoomID]*TypingController

	// Model catalog cache
	modelCatalogMu     sync.Mutex
	modelCatalogLoaded bool
	modelCatalogCache  []ModelCatalogEntry

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
		pendingQueues:       make(map[id.RoomID]*pendingQueue),
		activeRoomRuns:      make(map[id.RoomID]*roomRunState),
		groupHistoryBuffers: make(map[id.RoomID]*groupHistoryBuffer),
		queueTyping:         make(map[id.RoomID]*TypingController),
		loginConfig:         cloneAILoginConfig(cfg),
	}
	oc.InitClientBase(login, oc)
	oc.HumanUserIDPrefix = "openai-user"
	oc.MessageIDPrefix = "ai"
	oc.MessageLogKey = "ai_msg_id"

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

	// Load AI-local runtime state from aidb instead of bridge login metadata.
	oc.ensureLoginStateLoaded(context.Background())

	return oc, nil
}

func (oc *AIClient) GetApprovalHandler() sdk.ApprovalReactionHandler {
	return nil
}

func (oc *AIClient) postSaveUserMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	meta *PortalMetadata,
	msg *database.Message,
) {
	if msg == nil {
		return
	}
	if portal == nil && msg.Room.ID != "" && oc != nil && oc.UserLogin != nil && oc.UserLogin.Bridge != nil {
		resolved, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, msg.Room)
		if err != nil {
			oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to resolve portal for AI turn persistence")
		} else {
			portal = resolved
		}
	}
	if portal == nil {
		oc.loggerForContext(ctx).Debug().
			Str("message_id", string(msg.ID)).
			Str("event_id", msg.MXID.String()).
			Str("room_id", string(msg.Room.ID)).
			Str("room_receiver", string(msg.Room.Receiver)).
			Msg("Skipping AI turn persistence because portal lookup returned nil")
		return
	}
	metaSummary := "meta=nil"
	if msgMeta, _ := msg.Metadata.(*MessageMetadata); msgMeta != nil {
		metaSummary = transcriptMetaSummary(msgMeta)
	}
	oc.loggerForContext(ctx).Debug().
		Str("message_id", string(msg.ID)).
		Str("event_id", msg.MXID.String()).
		Str("resolved_portal_id", string(portal.PortalKey.ID)).
		Str("resolved_portal_receiver", string(portal.PortalKey.Receiver)).
		Str("resolved_portal_mxid", portal.MXID.String()).
		Str("meta", metaSummary).
		Msg("Persisting AI turn after bridgev2 message save")
	if err := oc.persistAIConversationMessage(ctx, portal, msg); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to persist AI conversation turn")
	}
}

func (oc *AIClient) persistAcceptedUserMessage(
	ctx context.Context,
	portal *bridgev2.Portal,
	msg *database.Message,
) {
	if msg == nil {
		return
	}
	meta, _ := msg.Metadata.(*MessageMetadata)
	oc.loggerForContext(ctx).Debug().
		Str("message_id", string(msg.ID)).
		Str("event_id", msg.MXID.String()).
		Str("room_id", string(msg.Room.ID)).
		Str("room_receiver", string(msg.Room.Receiver)).
		Str("sender_id", string(msg.SenderID)).
		Str("meta", transcriptMetaSummary(meta)).
		Msg("Persisting accepted user turn")
	if _, err := oc.UserLogin.Bridge.GetGhostByID(ctx, msg.SenderID); err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to ensure user ghost before saving message")
	}
	var err error
	if portal == nil {
		portal, err = oc.UserLogin.Bridge.GetPortalByKey(ctx, msg.Room)
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
	}
	oc.postSaveUserMessage(ctx, portal, nil, msg)
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

	oc.pendingQueuesMu.Lock()
	clear(oc.pendingQueues)
	oc.pendingQueuesMu.Unlock()

	oc.activeRoomRunsMu.Lock()
	clear(oc.activeRoomRuns)
	oc.activeRoomRunsMu.Unlock()

	oc.groupHistoryMu.Lock()
	clear(oc.groupHistoryBuffers)
	oc.groupHistoryMu.Unlock()

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

func (oc *AIClient) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	return oc.chatInfoFromPortal(ctx, portal), nil
}

func (oc *AIClient) GetUserInfo(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	ghostID := string(ghost.ID)

	// Parse model from ghost ID (format: "model-{escaped-model-id}")
	if modelID := parseModelFromGhostID(ghostID); modelID != "" {
		target := resolveTargetFromGhostID(ghost.ID)
		if responder, err := oc.resolveResponder(ctx, &PortalMetadata{ResolvedTarget: target}, ResponderResolveOptions{}); err == nil && responder != nil {
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
