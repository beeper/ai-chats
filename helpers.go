package agentremote

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/format"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/matrixevents"
	"github.com/beeper/agentremote/turns"
)

const AIRoomKindAgent = "agent"

func BuildMetaTypes(portal, message, userLogin, ghost func() any) database.MetaTypes {
	return database.MetaTypes{
		Portal:    portal,
		Message:   message,
		UserLogin: userLogin,
		Ghost:     ghost,
	}
}

// BuildSystemNotice creates a ConvertedMessage containing a single MsgNotice part.
func BuildSystemNotice(body string) *bridgev2.ConvertedMessage {
	return &bridgev2.ConvertedMessage{
		Parts: []*bridgev2.ConvertedMessagePart{{
			ID:   networkid.PartID("0"),
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType:  event.MsgNotice,
				Body:     body,
				Mentions: &event.Mentions{},
			},
		}},
	}
}

// SendDebouncedStreamEditParams holds the parameters for SendDebouncedStreamEdit.
type SendDebouncedStreamEditParams struct {
	Login            *bridgev2.UserLogin
	Portal           *bridgev2.Portal
	Sender           bridgev2.EventSender
	NetworkMessageID networkid.MessageID
	SuppressSend     bool
	VisibleBody      string
	FallbackBody     string
	LogKey           string
	Force            bool
	UIMessage        map[string]any
}

// SendDebouncedStreamEdit builds and queues a debounced stream edit via the bridge pipeline.
func SendDebouncedStreamEdit(p SendDebouncedStreamEditParams) error {
	if p.Login == nil || p.Portal == nil {
		return nil
	}
	content := turns.BuildDebouncedEditContent(turns.DebouncedEditParams{
		PortalMXID:   p.Portal.MXID.String(),
		Force:        p.Force,
		SuppressSend: p.SuppressSend,
		VisibleBody:  p.VisibleBody,
		FallbackBody: p.FallbackBody,
	})
	if content == nil || p.NetworkMessageID == "" {
		return nil
	}
	timing := ResolveEventTiming(time.Now(), 0)
	topLevelExtra := map[string]any{
		"com.beeper.dont_render_edited": true,
		"m.mentions":                    map[string]any{},
	}
	if len(p.UIMessage) > 0 {
		topLevelExtra[matrixevents.BeeperAIKey] = p.UIMessage
	}
	p.Login.QueueRemoteEvent(&RemoteEdit{
		Portal:        p.Portal.PortalKey,
		Sender:        p.Sender,
		TargetMessage: p.NetworkMessageID,
		Timestamp:     timing.Timestamp,
		StreamOrder:   timing.StreamOrder,
		LogKey:        p.LogKey,
		PreBuilt:      turns.BuildRenderedConvertedEdit(*content, topLevelExtra),
	})
	return nil
}

// DMChatInfoParams holds the parameters for BuildDMChatInfo.
type DMChatInfoParams struct {
	Title          string
	HumanUserID    networkid.UserID
	LoginID        networkid.UserLoginID
	BotUserID      networkid.UserID
	BotDisplayName string
	CanBackfill    bool
}

// BuildDMChatInfo creates a ChatInfo for a DM room between a human user and a bot ghost.
func BuildDMChatInfo(p DMChatInfoParams) *bridgev2.ChatInfo {
	members := bridgev2.ChatMemberMap{
		p.HumanUserID: {
			EventSender: bridgev2.EventSender{
				Sender:      p.HumanUserID,
				IsFromMe:    true,
				SenderLogin: p.LoginID,
			},
			Membership: event.MembershipJoin,
		},
		p.BotUserID: {
			EventSender: bridgev2.EventSender{
				Sender:      p.BotUserID,
				SenderLogin: p.LoginID,
			},
			Membership: event.MembershipJoin,
			UserInfo: &bridgev2.UserInfo{
				Name:  ptr.Ptr(p.BotDisplayName),
				IsBot: ptr.Ptr(true),
			},
			MemberEventExtra: map[string]any{
				"displayname": p.BotDisplayName,
			},
		},
	}
	return &bridgev2.ChatInfo{
		Name:        ptr.Ptr(p.Title),
		Type:        ptr.Ptr(database.RoomTypeDM),
		CanBackfill: p.CanBackfill,
		Members: &bridgev2.ChatMemberList{
			IsFull:      true,
			OtherUserID: p.BotUserID,
			MemberMap:   members,
		},
	}
}

type LoginDMChatInfoParams struct {
	Title             string
	Login             *bridgev2.UserLogin
	HumanUserIDPrefix string
	BotUserID         networkid.UserID
	BotDisplayName    string
	CanBackfill       bool
}

func BuildLoginDMChatInfo(p LoginDMChatInfoParams) *bridgev2.ChatInfo {
	if p.Login == nil {
		return nil
	}
	return BuildDMChatInfo(DMChatInfoParams{
		Title:          p.Title,
		HumanUserID:    HumanUserID(p.HumanUserIDPrefix, p.Login.ID),
		LoginID:        p.Login.ID,
		BotUserID:      p.BotUserID,
		BotDisplayName: p.BotDisplayName,
		CanBackfill:    p.CanBackfill,
	})
}

// SendViaPortalParams holds the parameters for SendViaPortal.
type SendViaPortalParams struct {
	Login     *bridgev2.UserLogin
	Portal    *bridgev2.Portal
	Sender    bridgev2.EventSender
	IDPrefix  string // e.g. "ai", "codex", "opencode"
	LogKey    string // zerolog field name, e.g. "ai_msg_id"
	MsgID     networkid.MessageID
	Timestamp time.Time
	// StreamOrder is optional explicit ordering for events that share a timestamp.
	StreamOrder int64
	Converted   *bridgev2.ConvertedMessage
}

// SendViaPortal sends a pre-built message through bridgev2's QueueRemoteEvent pipeline.
// If MsgID is empty, a new one is generated using IDPrefix.
func SendViaPortal(p SendViaPortalParams) (id.EventID, networkid.MessageID, error) {
	if p.Portal == nil || p.Portal.MXID == "" {
		return "", "", fmt.Errorf("invalid portal")
	}
	if p.Login == nil || p.Login.Bridge == nil {
		return "", p.MsgID, fmt.Errorf("bridge unavailable")
	}
	if p.MsgID == "" {
		p.MsgID = NewMessageID(p.IDPrefix)
	}
	timing := ResolveEventTiming(p.Timestamp, p.StreamOrder)
	evt := &simplevent.PreConvertedMessage{
		EventMeta: simplevent.EventMeta{
			Type:        bridgev2.RemoteEventMessage,
			PortalKey:   p.Portal.PortalKey,
			Sender:      p.Sender,
			Timestamp:   timing.Timestamp,
			StreamOrder: timing.StreamOrder,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str(p.LogKey, string(p.MsgID))
			},
		},
		ID:   p.MsgID,
		Data: p.Converted,
	}
	result := p.Login.QueueRemoteEvent(evt)
	if !result.Success {
		if result.Error != nil {
			return "", p.MsgID, fmt.Errorf("send failed: %w", result.Error)
		}
		return "", p.MsgID, fmt.Errorf("send failed")
	}
	return result.EventID, p.MsgID, nil
}

// SendEditViaPortal queues a pre-built edit through bridgev2's remote event pipeline.
func SendEditViaPortal(
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	targetMessage networkid.MessageID,
	timestamp time.Time,
	streamOrder int64,
	logKey string,
	converted *bridgev2.ConvertedEdit,
) error {
	if portal == nil || portal.MXID == "" {
		return fmt.Errorf("invalid portal")
	}
	if login == nil || login.Bridge == nil {
		return fmt.Errorf("bridge unavailable")
	}
	if targetMessage == "" {
		return fmt.Errorf("invalid target message")
	}
	timing := ResolveEventTiming(timestamp, streamOrder)
	result := login.QueueRemoteEvent(&RemoteEdit{
		Portal:        portal.PortalKey,
		Sender:        sender,
		TargetMessage: targetMessage,
		Timestamp:     timing.Timestamp,
		StreamOrder:   timing.StreamOrder,
		LogKey:        logKey,
		PreBuilt:      converted,
	})
	if !result.Success {
		if result.Error != nil {
			return fmt.Errorf("edit failed: %w", result.Error)
		}
		return fmt.Errorf("edit failed")
	}
	return nil
}

// RedactEventAsSender redacts an event ID in a room using the intent resolved for sender.
func RedactEventAsSender(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	targetEventID id.EventID,
) error {
	if login == nil || portal == nil || portal.MXID == "" || targetEventID == "" {
		return fmt.Errorf("invalid redaction target")
	}
	intent, ok := portal.GetIntentFor(ctx, sender, login, bridgev2.RemoteEventMessageRemove)
	if !ok || intent == nil {
		return fmt.Errorf("intent resolution failed")
	}
	_, err := intent.SendMessage(ctx, portal.MXID, event.EventRedaction, &event.Content{
		Parsed: &event.RedactionEventContent{Redacts: targetEventID},
	}, nil)
	return err
}

func BuildChatInfoWithFallback(metaTitle, portalName, fallbackTitle, portalTopic string) *bridgev2.ChatInfo {
	title := coalesceStrings(metaTitle, portalName, fallbackTitle)
	return &bridgev2.ChatInfo{
		Name:  ptr.Ptr(title),
		Topic: ptr.NonZero(portalTopic),
	}
}

var MediaMessageTypes = []event.MessageType{
	event.MsgImage,
	event.MsgVideo,
	event.MsgAudio,
	event.MsgFile,
	event.CapMsgVoice,
	event.CapMsgGIF,
	event.CapMsgSticker,
}

type RoomFeaturesParams struct {
	ID                  string
	File                event.FileFeatureMap
	MaxTextLength       int
	Reply               event.CapabilitySupportLevel
	Thread              event.CapabilitySupportLevel
	Edit                event.CapabilitySupportLevel
	Delete              event.CapabilitySupportLevel
	Reaction            event.CapabilitySupportLevel
	ReadReceipts        bool
	TypingNotifications bool
	DeleteChat          bool
}

func BuildRoomFeatures(p RoomFeaturesParams) *event.RoomFeatures {
	return &event.RoomFeatures{
		ID:                  p.ID,
		File:                p.File,
		MaxTextLength:       p.MaxTextLength,
		Reply:               p.Reply,
		Thread:              p.Thread,
		Edit:                p.Edit,
		Delete:              p.Delete,
		Reaction:            p.Reaction,
		ReadReceipts:        p.ReadReceipts,
		TypingNotifications: p.TypingNotifications,
		DeleteChat:          p.DeleteChat,
	}
}

func BuildMediaFileFeatureMap(build func() *event.FileFeatures) event.FileFeatureMap {
	files := make(event.FileFeatureMap, len(MediaMessageTypes))
	for _, msgType := range MediaMessageTypes {
		files[msgType] = build()
	}
	return files
}

func ExpandUserHome(path string) (string, error) {
	rest, isTilde := strings.CutPrefix(strings.TrimSpace(path), "~")
	if !isTilde {
		return strings.TrimSpace(path), nil
	}
	if rest != "" && rest[0] != '/' {
		return strings.TrimSpace(path), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, rest), nil
}

func NormalizeAbsolutePath(path string) (string, error) {
	expanded, err := ExpandUserHome(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("path must be absolute")
	}
	return filepath.Clean(expanded), nil
}

// BuildBotUserInfo returns a UserInfo for an AI bot ghost with the given name and identifiers.
func BuildBotUserInfo(name string, identifiers ...string) *bridgev2.UserInfo {
	return &bridgev2.UserInfo{
		Name:        ptr.Ptr(name),
		IsBot:       ptr.Ptr(true),
		Identifiers: identifiers,
	}
}

func NormalizeAIRoomTypeV2(roomType database.RoomType, aiKind string) string {
	if aiKind != "" && aiKind != AIRoomKindAgent {
		return "group"
	}
	switch roomType {
	case database.RoomTypeDM:
		return "dm"
	case database.RoomTypeSpace:
		return "space"
	default:
		return "group"
	}
}

func ApplyAIBridgeInfo(content *event.BridgeEventContent, protocolID string, roomType database.RoomType, aiKind string) {
	if content == nil {
		return
	}
	if protocolID != "" {
		content.Protocol.ID = protocolID
	}
	content.BeeperRoomTypeV2 = NormalizeAIRoomTypeV2(roomType, aiKind)
}

func SendAIRoomInfo(ctx context.Context, portal *bridgev2.Portal, aiKind string) bool {
	if portal == nil || portal.MXID == "" {
		return false
	}
	if aiKind == "" {
		aiKind = AIRoomKindAgent
	}
	//lint:ignore SA1019 bridgev2 currently exposes room-meta sending via portal internals
	return portal.Internal().SendRoomMeta(
		ctx,
		nil,
		time.Now(),
		matrixevents.AIRoomInfoEventType,
		"",
		map[string]any{"type": aiKind},
		true,
		nil,
	)
}

// findExistingMessage performs a two-phase message lookup: first by network
// message ID (with receiver resolution), then by Matrix event ID as fallback.
// Returns the message (if found) and separate errors from each lookup phase.
func findExistingMessage(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	networkMessageID networkid.MessageID,
	initialEventID id.EventID,
) (msg *database.Message, errByID error, errByMXID error) {
	receiver := portal.Receiver
	if receiver == "" {
		receiver = login.ID
	}
	if receiver != "" && networkMessageID != "" {
		msg, errByID = login.Bridge.DB.Message.GetPartByID(ctx, receiver, networkMessageID, networkid.PartID("0"))
	}
	if msg == nil && initialEventID != "" {
		msg, errByMXID = login.Bridge.DB.Message.GetPartByMXID(ctx, initialEventID)
	}
	return msg, errByID, errByMXID
}

// UpsertAssistantMessageParams holds parameters for UpsertAssistantMessage.
type UpsertAssistantMessageParams struct {
	Login            *bridgev2.UserLogin
	Portal           *bridgev2.Portal
	SenderID         networkid.UserID
	NetworkMessageID networkid.MessageID
	InitialEventID   id.EventID
	Metadata         any // must satisfy database.MetaMerger
	Logger           zerolog.Logger
}

// UpsertAssistantMessage updates an existing message's metadata or inserts a new one.
// If NetworkMessageID is set, tries to find and update the existing row first.
// Falls back to inserting a new row keyed by InitialEventID.
func UpsertAssistantMessage(ctx context.Context, p UpsertAssistantMessageParams) {
	if p.Login == nil || p.Portal == nil {
		return
	}
	db := p.Login.Bridge.DB.Message

	if p.NetworkMessageID != "" {
		existing, errByID, errByMXID := findExistingMessage(ctx, p.Login, p.Portal, p.NetworkMessageID, p.InitialEventID)
		if existing != nil {
			existing.Metadata = p.Metadata
			if err := db.Update(ctx, existing); err != nil {
				p.Logger.Warn().Err(err).Str("msg_id", string(existing.ID)).Msg("Failed to update assistant message metadata")
			} else {
				p.Logger.Debug().Str("msg_id", string(existing.ID)).Msg("Updated assistant message metadata")
			}
			return
		}
		p.Logger.Warn().
			AnErr("err_by_id", errByID).
			AnErr("err_by_mxid", errByMXID).
			Stringer("mxid", p.InitialEventID).
			Str("msg_id", string(p.NetworkMessageID)).
			Msg("Could not find existing DB row for update, falling back to insert")
	}

	if p.InitialEventID == "" {
		return
	}
	assistantMsg := &database.Message{
		ID:        MatrixMessageID(p.InitialEventID),
		Room:      p.Portal.PortalKey,
		SenderID:  p.SenderID,
		MXID:      p.InitialEventID,
		Timestamp: time.Now(),
		Metadata:  p.Metadata,
	}
	if err := db.Insert(ctx, assistantMsg); err != nil {
		p.Logger.Warn().Err(err).Msg("Failed to insert assistant message to database")
	} else {
		p.Logger.Debug().Str("msg_id", string(assistantMsg.ID)).Msg("Inserted assistant message to database")
	}
}

// DefaultApprovalExpiry is the fallback expiry duration when no TTL is specified.
const DefaultApprovalExpiry = 10 * time.Minute

// ComputeApprovalExpiry returns the expiry time based on ttlSeconds, falling
// back to DefaultApprovalExpiry when ttlSeconds <= 0.
func ComputeApprovalExpiry(ttlSeconds int) time.Time {
	if ttlSeconds > 0 {
		return time.Now().Add(time.Duration(ttlSeconds) * time.Second)
	}
	return time.Now().Add(DefaultApprovalExpiry)
}

// BuildContinuationMessage constructs a ConvertedMessage for overflow
// continuation text, flagged with "com.beeper.continuation".
func BuildContinuationMessage(
	portal networkid.PortalKey,
	body string,
	sender bridgev2.EventSender,
	idPrefix,
	logKey string,
	timestamp time.Time,
	streamOrder int64,
) *simplevent.PreConvertedMessage {
	rendered := format.RenderMarkdown(body, true, true)
	raw := map[string]any{
		"msgtype":                 event.MsgText,
		"body":                    rendered.Body,
		"format":                  rendered.Format,
		"formatted_body":          rendered.FormattedBody,
		"com.beeper.continuation": true,
		"m.mentions":              map[string]any{},
	}
	msgID := NewMessageID(idPrefix)
	timing := ResolveEventTiming(timestamp, streamOrder)
	return &simplevent.PreConvertedMessage{
		EventMeta: simplevent.EventMeta{
			Type:        bridgev2.RemoteEventMessage,
			PortalKey:   portal,
			Sender:      sender,
			Timestamp:   timing.Timestamp,
			StreamOrder: timing.StreamOrder,
			LogContext: func(c zerolog.Context) zerolog.Context {
				return c.Str(logKey, string(msgID))
			},
		},
		ID: msgID,
		Data: &bridgev2.ConvertedMessage{
			Parts: []*bridgev2.ConvertedMessagePart{{
				ID:      networkid.PartID("0"),
				Type:    event.EventMessage,
				Content: &event.MessageEventContent{MsgType: event.MsgText, Body: body},
				Extra:   raw,
			}},
		},
	}
}

// coalesceStrings returns the first non-empty string from the arguments.
func coalesceStrings(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
