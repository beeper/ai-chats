package sdk

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

// Conversation represents a chat room the agent is participating in.
type Conversation struct {
	ID    string
	Title string

	ctx     context.Context
	portal  *bridgev2.Portal
	login   *bridgev2.UserLogin
	sender  bridgev2.EventSender
	runtime conversationRuntime

	intentOverride func(context.Context) (bridgev2.MatrixAPI, error)
}

func newConversation(ctx context.Context, portal *bridgev2.Portal, login *bridgev2.UserLogin, sender bridgev2.EventSender, runtime conversationRuntime) *Conversation {
	conv := &Conversation{
		ctx:     ctx,
		portal:  portal,
		login:   login,
		sender:  sender,
		runtime: runtime,
	}
	if portal != nil {
		conv.ID = string(portal.ID)
		conv.Title = portal.Name
	}
	return conv
}

func (c *Conversation) getIntent(ctx context.Context) (bridgev2.MatrixAPI, error) {
	if c == nil {
		return nil, fmt.Errorf("conversation is nil")
	}
	if c.intentOverride != nil {
		return c.intentOverride(ctx)
	}
	return resolveMatrixIntent(ctx, c.login, c.portal, c.sender, bridgev2.RemoteEventMessage)
}

func (c *Conversation) stateStore() *conversationStateStore {
	if c == nil || c.runtime == nil {
		return nil
	}
	return c.runtime.conversationStore()
}

func (c *Conversation) state() *sdkConversationState {
	if c == nil {
		return &sdkConversationState{}
	}
	return loadConversationState(c.portal, c.stateStore())
}

func (c *Conversation) saveState(ctx context.Context, state *sdkConversationState) error {
	if c == nil {
		return nil
	}
	return saveConversationState(ctx, c.portal, c.stateStore(), state)
}

func (c *Conversation) resolveDefaultAgent(ctx context.Context) (*Agent, error) {
	if c == nil {
		return nil, nil
	}
	for _, agentID := range c.state().RoomAgents.AgentIDs {
		if agent, err := c.resolveAgentByIdentifier(ctx, agentID); err == nil && agent != nil {
			return agent, nil
		}
	}
	if c.runtime == nil {
		return nil, nil
	}
	if agent := c.runtime.agent(); agent != nil {
		return agent, nil
	}
	if catalog := c.runtime.agentCatalog(); catalog != nil {
		return catalog.DefaultAgent(ctx, c.login)
	}
	return nil, nil
}

func (c *Conversation) resolveAgentByIdentifier(ctx context.Context, identifier string) (*Agent, error) {
	if c == nil || strings.TrimSpace(identifier) == "" {
		return nil, nil
	}
	if c.runtime == nil {
		return nil, nil
	}
	if agent := c.runtime.agent(); agent != nil && agent.ID == identifier {
		return agent, nil
	}
	if catalog := c.runtime.agentCatalog(); catalog != nil {
		return catalog.ResolveAgent(ctx, c.login, identifier)
	}
	return nil, nil
}

func (c *Conversation) currentRoomFeatures(ctx context.Context) *RoomFeatures {
	if c == nil {
		return nil
	}
	if c.runtime != nil {
		if rf := c.runtime.roomFeatures(c); rf != nil {
			return rf
		}
	}
	state := c.state()
	agents := make([]*Agent, 0, len(state.RoomAgents.AgentIDs))
	for _, agentID := range state.RoomAgents.AgentIDs {
		agent, err := c.resolveAgentByIdentifier(ctx, agentID)
		if err != nil || agent == nil {
			continue
		}
		agents = append(agents, agent)
	}
	if len(agents) == 0 {
		if defaultAgent, err := c.resolveDefaultAgent(ctx); err == nil && defaultAgent != nil {
			agents = append(agents, defaultAgent)
		}
	}
	if len(agents) == 0 {
		return defaultSDKFeatureConfig()
	}
	return computeRoomFeaturesForAgents(agents)
}

// SendHTML sends a message with both plaintext and HTML body.
func (c *Conversation) SendHTML(ctx context.Context, text, html string) error {
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    text,
	}
	if html != "" {
		content.Format = event.FormatHTML
		content.FormattedBody = html
	}
	return c.sendMessageContent(ctx, content)
}

// SendMedia sends a media message.
func (c *Conversation) SendMedia(ctx context.Context, data []byte, mediaType, filename string) error {
	intent, err := c.getIntent(ctx)
	if err != nil {
		return err
	}
	mxcURL, encFile, err := intent.UploadMedia(ctx, c.portal.MXID, data, filename, mediaType)
	if err != nil {
		return err
	}
	msgType := event.MsgFile
	switch {
	case strings.HasPrefix(mediaType, "image/"):
		msgType = event.MsgImage
	case strings.HasPrefix(mediaType, "audio/"):
		msgType = event.MsgAudio
	case strings.HasPrefix(mediaType, "video/"):
		msgType = event.MsgVideo
	}
	content := &event.MessageEventContent{
		MsgType: msgType,
		Body:    filename,
		Info: &event.FileInfo{
			MimeType: mediaType,
			Size:     len(data),
		},
	}
	if encFile != nil {
		content.File = encFile
	} else {
		content.URL = mxcURL
	}
	wrappedContent := &event.Content{Parsed: content}
	_, err = intent.SendMessage(ctx, c.portal.MXID, event.EventMessage, wrappedContent, nil)
	return err
}

// SendNotice sends a notice message.
func (c *Conversation) SendNotice(ctx context.Context, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return c.sendMessageContent(ctx, &event.MessageEventContent{
		MsgType:  event.MsgNotice,
		Body:     text,
		Mentions: &event.Mentions{},
	})
}

func (c *Conversation) sendMessageContent(ctx context.Context, content *event.MessageEventContent) error {
	intent, err := c.getIntent(ctx)
	if err != nil {
		return err
	}
	_, err = intent.SendMessage(ctx, c.portal.MXID, event.EventMessage, &event.Content{Parsed: content}, nil)
	return err
}

// Stream starts a new streaming response in this conversation.
func (c *Conversation) Stream(ctx context.Context) *Turn {
	return newTurn(ctx, c, nil, nil)
}

// StartTurn creates a new Turn for this conversation.
func (c *Conversation) StartTurn(ctx context.Context, agent *Agent, source *SourceRef) *Turn {
	return newTurn(ctx, c, agent, source)
}

// Context returns the conversation's context.
func (c *Conversation) Context() context.Context {
	return c.ctx
}

// Spec returns the current persisted conversation spec snapshot.
func (c *Conversation) Spec() ConversationSpec {
	state := c.state()
	return ConversationSpec{
		PortalID:             c.ID,
		Kind:                 state.Kind,
		Visibility:           state.Visibility,
		ParentConversationID: state.ParentConversationID,
		ParentEventID:        state.ParentEventID,
		Title:                c.Title,
		ArchiveOnCompletion:  state.ArchiveOnCompletion,
		Metadata:             maps.Clone(state.Metadata),
	}
}

// EnsureRoomAgent ensures the agent is part of the room agent set.
func (c *Conversation) EnsureRoomAgent(ctx context.Context, agent *Agent) error {
	if c == nil || agent == nil {
		return nil
	}
	if err := agent.EnsureGhost(ctx, c.login); err != nil {
		return err
	}
	state := c.state()
	state.RoomAgents.AgentIDs = append(state.RoomAgents.AgentIDs, agent.ID)
	state.RoomAgents.AgentIDs = normalizeAgentIDs(state.RoomAgents.AgentIDs)
	if err := c.saveState(ctx, state); err != nil {
		return err
	}
	if c.portal != nil && c.login != nil {
		c.portal.UpdateCapabilities(ctx, c.login, false)
	}
	return nil
}

// RoomAgents returns the current room agent set.
func (c *Conversation) RoomAgents(ctx context.Context) (*RoomAgentSet, error) {
	state := c.state()
	if len(state.RoomAgents.AgentIDs) == 0 {
		defaultAgent, err := c.resolveDefaultAgent(ctx)
		if err != nil {
			return nil, err
		}
		if defaultAgent != nil {
			state.RoomAgents.AgentIDs = []string{defaultAgent.ID}
			_ = c.saveState(ctx, state)
		}
	}
	result := state.RoomAgents
	result.AgentIDs = slices.Clone(result.AgentIDs)
	return &result, nil
}

// SetTyping sets the typing indicator for this conversation.
func (c *Conversation) SetTyping(ctx context.Context, typing bool) error {
	intent, err := c.getIntent(ctx)
	if err != nil {
		return err
	}
	timeout := 30 * time.Second
	if !typing {
		timeout = 0
	}
	return intent.MarkTyping(ctx, c.portal.MXID, bridgev2.TypingTypeText, timeout)
}

// SetRoomName sets the room name.
func (c *Conversation) SetRoomName(ctx context.Context, name string) error {
	return SetRoomName(ctx, c.login, c.portal, c.sender, name)
}

// SetRoomTopic sets the room topic.
func (c *Conversation) SetRoomTopic(ctx context.Context, topic string) error {
	return SetRoomTopic(ctx, c.login, c.portal, c.sender, topic)
}

// BroadcastCapabilities computes and sends room capability state events.
func (c *Conversation) BroadcastCapabilities(ctx context.Context) error {
	features := c.currentRoomFeatures(ctx)
	return BroadcastCapabilities(ctx, c.login, c.portal, c.sender, features)
}

// Portal returns the underlying bridgev2.Portal.
func (c *Conversation) Portal() *bridgev2.Portal { return c.portal }

// Login returns the underlying bridgev2.UserLogin.
func (c *Conversation) Login() *bridgev2.UserLogin { return c.login }

// Sender returns the event sender for this conversation.
func (c *Conversation) Sender() bridgev2.EventSender { return c.sender }

// QueueRemoteEvent queues a remote event for processing.
func (c *Conversation) QueueRemoteEvent(evt bridgev2.RemoteEvent) {
	if c.login != nil {
		c.login.Bridge.QueueRemoteEvent(c.login, evt)
	}
}

func normalizeConversationSpec(spec ConversationSpec) ConversationSpec {
	if spec.Kind == "" {
		spec.Kind = ConversationKindNormal
	}
	if spec.Kind == ConversationKindDelegated {
		if spec.Visibility == "" {
			spec.Visibility = ConversationVisibilityHidden
		}
		spec.ArchiveOnCompletion = true
	}
	if spec.Visibility == "" {
		spec.Visibility = ConversationVisibilityNormal
	}
	if strings.TrimSpace(spec.PortalID) == "" {
		spec.PortalID = "sdk:" + uuid.NewString()
	}
	return spec
}

func conversationStateFromSpec(spec ConversationSpec) *sdkConversationState {
	spec = normalizeConversationSpec(spec)
	return &sdkConversationState{
		Kind:                 spec.Kind,
		Visibility:           spec.Visibility,
		ParentConversationID: strings.TrimSpace(spec.ParentConversationID),
		ParentEventID:        strings.TrimSpace(spec.ParentEventID),
		ArchiveOnCompletion:  spec.ArchiveOnCompletion,
		Metadata:             spec.Metadata,
	}
}

func ensureConversationPortal(ctx context.Context, login *bridgev2.UserLogin, spec ConversationSpec) (*bridgev2.Portal, error) {
	if login == nil || login.Bridge == nil {
		return nil, fmt.Errorf("login bridge unavailable")
	}
	spec = normalizeConversationSpec(spec)
	key := networkid.PortalKey{
		ID: networkid.PortalID(spec.PortalID),
	}
	if login.ID != "" {
		key.Receiver = login.ID
	}
	portal, err := login.Bridge.GetPortalByKey(ctx, key)
	if err != nil {
		return nil, err
	}
	if portal.RoomType == "" {
		portal.RoomType = database.RoomTypeDefault
	}
	if strings.TrimSpace(spec.Title) != "" {
		portal.Name = strings.TrimSpace(spec.Title)
		portal.NameSet = true
	}
	return portal, nil
}
