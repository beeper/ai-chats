package openclaw

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type OpenClawSessionResyncEvent struct {
	client  *OpenClawClient
	session gatewaySessionRow
}

var (
	_ bridgev2.RemoteChatResyncWithInfo       = (*OpenClawSessionResyncEvent)(nil)
	_ bridgev2.RemoteEventThatMayCreatePortal = (*OpenClawSessionResyncEvent)(nil)
)

func (evt *OpenClawSessionResyncEvent) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventChatResync
}

func (evt *OpenClawSessionResyncEvent) ShouldCreatePortal() bool {
	return true
}

func (evt *OpenClawSessionResyncEvent) GetPortalKey() networkid.PortalKey {
	return evt.client.portalKeyForSession(evt.session.Key)
}

func (evt *OpenClawSessionResyncEvent) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("session_key", evt.session.Key).Str("session_id", evt.session.SessionID)
}

func (evt *OpenClawSessionResyncEvent) GetSender() bridgev2.EventSender {
	return bridgev2.EventSender{}
}

func (evt *OpenClawSessionResyncEvent) GetChatInfo(ctx context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	if portal == nil {
		return nil, fmt.Errorf("missing portal")
	}
	meta := portalMeta(portal)
	meta.IsOpenClawRoom = true
	meta.OpenClawGatewayID = evt.client.gatewayID()
	meta.OpenClawSessionID = evt.session.SessionID
	meta.OpenClawSessionKey = evt.session.Key
	meta.OpenClawSessionKind = evt.session.Kind
	meta.OpenClawSessionLabel = evt.session.Label
	meta.OpenClawDisplayName = evt.session.DisplayName
	meta.OpenClawDerivedTitle = evt.session.DerivedTitle
	meta.OpenClawLastMessagePreview = evt.session.LastMessagePreview
	meta.OpenClawChannel = evt.session.Channel
	meta.OpenClawSubject = evt.session.Subject
	meta.OpenClawGroupChannel = evt.session.GroupChannel
	meta.OpenClawSpace = evt.session.Space
	meta.OpenClawChatType = evt.session.ChatType
	meta.OpenClawOrigin = evt.session.Origin
	meta.OpenClawAgentID = stringsTrimDefault(meta.OpenClawAgentID, openClawAgentIDFromSessionKey(evt.session.Key))
	meta.OpenClawSystemSent = evt.session.SystemSent
	meta.OpenClawAbortedLastRun = evt.session.AbortedLastRun
	meta.ThinkingLevel = evt.session.ThinkingLevel
	meta.VerboseLevel = evt.session.VerboseLevel
	meta.ReasoningLevel = evt.session.ReasoningLevel
	meta.ElevatedLevel = evt.session.ElevatedLevel
	meta.SendPolicy = evt.session.SendPolicy
	meta.InputTokens = evt.session.InputTokens
	meta.OutputTokens = evt.session.OutputTokens
	meta.TotalTokens = evt.session.TotalTokens
	meta.TotalTokensFresh = evt.session.TotalTokensFresh
	meta.ResponseUsage = evt.session.ResponseUsage
	meta.ModelProvider = evt.session.ModelProvider
	meta.Model = evt.session.Model
	meta.ContextTokens = evt.session.ContextTokens
	meta.DeliveryContext = evt.session.DeliveryContext
	meta.LastChannel = evt.session.LastChannel
	meta.LastTo = evt.session.LastTo
	meta.LastAccountID = evt.session.LastAccountID
	meta.SessionUpdatedAt = evt.session.UpdatedAt
	meta.HistoryMode = "recent_only"
	meta.RecentHistoryLimit = openClawDefaultSessionLimit
	portal.Metadata = meta

	title := evt.client.displayNameForSession(evt.session)
	memberMap := bridgev2.ChatMemberMap{
		humanUserID(evt.client.UserLogin.ID): {
			EventSender: bridgev2.EventSender{
				Sender:      humanUserID(evt.client.UserLogin.ID),
				SenderLogin: evt.client.UserLogin.ID,
				IsFromMe:    true,
			},
		},
	}
	agentID := stringsTrimDefault(meta.OpenClawAgentID, "gateway")
	identity := evt.client.lookupAgentIdentity(ctx, agentID, evt.session.Key)
	if identity != nil && strings.TrimSpace(identity.AgentID) != "" {
		agentID = strings.TrimSpace(identity.AgentID)
		meta.OpenClawAgentID = agentID
	}
	agentName := evt.client.displayNameForAgent(agentID)
	if identity != nil {
		if strings.TrimSpace(identity.Name) != "" {
			agentName = evt.client.formatAgentDisplayName(&GhostMetadata{
				OpenClawAgentID:    agentID,
				OpenClawAgentName:  strings.TrimSpace(identity.Name),
				OpenClawAgentEmoji: strings.TrimSpace(identity.Emoji),
			}, agentID)
		}
	}
	memberMap[openClawGhostUserID(agentID)] = bridgev2.ChatMember{
		EventSender: evt.client.senderForAgent(agentID, false),
		UserInfo: &bridgev2.UserInfo{
			Name:  ptr.Ptr(agentName),
			IsBot: ptr.Ptr(true),
		},
	}
	return &bridgev2.ChatInfo{
		Type:  ptr.Ptr(database.RoomTypeDM),
		Name:  ptr.Ptr(title),
		Topic: ptr.NonZero(evt.client.topicForPortal(meta)),
		Members: &bridgev2.ChatMemberList{
			IsFull:    true,
			MemberMap: memberMap,
		},
	}, nil
}

type OpenClawRemoteMessage struct {
	portal    networkid.PortalKey
	id        networkid.MessageID
	sender    bridgev2.EventSender
	timestamp time.Time
	preBuilt  *bridgev2.ConvertedMessage
}

var (
	_ bridgev2.RemoteMessage              = (*OpenClawRemoteMessage)(nil)
	_ bridgev2.RemoteEventWithTimestamp   = (*OpenClawRemoteMessage)(nil)
	_ bridgev2.RemoteEventWithStreamOrder = (*OpenClawRemoteMessage)(nil)
)

func (m *OpenClawRemoteMessage) GetType() bridgev2.RemoteEventType {
	return bridgev2.RemoteEventMessage
}
func (m *OpenClawRemoteMessage) GetPortalKey() networkid.PortalKey { return m.portal }
func (m *OpenClawRemoteMessage) GetSender() bridgev2.EventSender   { return m.sender }
func (m *OpenClawRemoteMessage) GetID() networkid.MessageID        { return m.id }
func (m *OpenClawRemoteMessage) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("openclaw_msg_id", string(m.id))
}
func (m *OpenClawRemoteMessage) GetTimestamp() time.Time {
	if m.timestamp.IsZero() {
		return time.Now()
	}
	return m.timestamp
}
func (m *OpenClawRemoteMessage) GetStreamOrder() int64 {
	return m.GetTimestamp().UnixMilli()
}
func (m *OpenClawRemoteMessage) ConvertMessage(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI) (*bridgev2.ConvertedMessage, error) {
	return m.preBuilt, nil
}

type OpenClawRemoteEdit struct {
	portal        networkid.PortalKey
	sender        bridgev2.EventSender
	targetMessage networkid.MessageID
	timestamp     time.Time
	preBuilt      *bridgev2.ConvertedEdit
}

var (
	_ bridgev2.RemoteEdit                 = (*OpenClawRemoteEdit)(nil)
	_ bridgev2.RemoteEventWithTimestamp   = (*OpenClawRemoteEdit)(nil)
	_ bridgev2.RemoteEventWithStreamOrder = (*OpenClawRemoteEdit)(nil)
)

func (e *OpenClawRemoteEdit) GetType() bridgev2.RemoteEventType { return bridgev2.RemoteEventEdit }
func (e *OpenClawRemoteEdit) GetPortalKey() networkid.PortalKey { return e.portal }
func (e *OpenClawRemoteEdit) GetSender() bridgev2.EventSender   { return e.sender }
func (e *OpenClawRemoteEdit) GetTargetMessage() networkid.MessageID {
	return e.targetMessage
}
func (e *OpenClawRemoteEdit) AddLogContext(c zerolog.Context) zerolog.Context {
	return c.Str("openclaw_edit_target", string(e.targetMessage))
}
func (e *OpenClawRemoteEdit) GetTimestamp() time.Time {
	if e.timestamp.IsZero() {
		return time.Now()
	}
	return e.timestamp
}
func (e *OpenClawRemoteEdit) GetStreamOrder() int64 {
	return e.GetTimestamp().UnixMilli()
}
func (e *OpenClawRemoteEdit) ConvertEdit(_ context.Context, _ *bridgev2.Portal, _ bridgev2.MatrixAPI, existing []*database.Message) (*bridgev2.ConvertedEdit, error) {
	if e.preBuilt != nil && len(existing) > 0 {
		for i, part := range e.preBuilt.ModifiedParts {
			if part.Part == nil && i < len(existing) {
				part.Part = existing[i]
			}
		}
	}
	return e.preBuilt, nil
}

func newOpenClawMessageID() networkid.MessageID {
	return networkid.MessageID("openclaw:" + uuid.NewString())
}
