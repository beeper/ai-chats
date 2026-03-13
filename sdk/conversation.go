package sdk

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
)

// Conversation represents a chat room the agent is participating in.
type Conversation struct {
	ID    string
	Title string

	ctx    context.Context
	portal *bridgev2.Portal
	login  *bridgev2.UserLogin
	sender bridgev2.EventSender
	client *sdkClient
}

func newConversation(ctx context.Context, portal *bridgev2.Portal, login *bridgev2.UserLogin, sender bridgev2.EventSender, client *sdkClient) *Conversation {
	id := ""
	title := ""
	if portal != nil {
		id = string(portal.ID)
		title = portal.Name
	}
	return &Conversation{
		ID:     id,
		Title:  title,
		ctx:    ctx,
		portal: portal,
		login:  login,
		sender: sender,
		client: client,
	}
}

func (c *Conversation) getIntent(ctx context.Context) (bridgev2.MatrixAPI, error) {
	if c.portal == nil || c.login == nil {
		return nil, fmt.Errorf("no portal or login")
	}
	intent, ok := c.portal.GetIntentFor(ctx, c.sender, c.login, bridgev2.RemoteEventMessage)
	if !ok || intent == nil {
		return nil, fmt.Errorf("failed to get intent")
	}
	return intent, nil
}

func (c *Conversation) state() *sdkConversationState {
	if c == nil {
		return &sdkConversationState{}
	}
	if c.client != nil {
		return loadConversationState(c.portal, c.client.conversationState)
	}
	return loadConversationState(c.portal, nil)
}

func (c *Conversation) saveState(ctx context.Context, state *sdkConversationState) error {
	if c == nil {
		return nil
	}
	var store *conversationStateStore
	if c.client != nil {
		store = c.client.conversationState
	}
	return saveConversationState(ctx, c.portal, store, state)
}

func (c *Conversation) resolveDefaultAgent(ctx context.Context) (*Agent, error) {
	if c == nil {
		return nil, nil
	}
	state := c.state()
	if primary := strings.TrimSpace(state.RoomAgents.PrimaryAgentID); primary != "" {
		if agent, err := c.resolveAgentByIdentifier(ctx, primary); err == nil && agent != nil {
			return agent, nil
		}
	}
	if c.client != nil && c.client.cfg().Agent != nil {
		return c.client.cfg().Agent, nil
	}
	if c.client != nil && c.client.cfg().AgentCatalog != nil {
		return c.client.cfg().AgentCatalog.DefaultAgent(ctx, c.login)
	}
	return nil, nil
}

func (c *Conversation) resolveAgentByIdentifier(ctx context.Context, identifier string) (*Agent, error) {
	if c == nil || strings.TrimSpace(identifier) == "" {
		return nil, nil
	}
	if c.client != nil && c.client.cfg().Agent != nil && c.client.cfg().Agent.ID == identifier {
		return c.client.cfg().Agent, nil
	}
	if c.client != nil && c.client.cfg().AgentCatalog != nil {
		return c.client.cfg().AgentCatalog.ResolveAgent(ctx, c.login, identifier)
	}
	return nil, nil
}

func (c *Conversation) currentRoomFeatures(ctx context.Context) *RoomFeatures {
	if c == nil {
		return nil
	}
	if c.client != nil && c.client.cfg().GetCapabilities != nil {
		if rf := c.client.cfg().GetCapabilities(c.client.getSession(), c); rf != nil {
			return rf
		}
	}
	state := c.state()
	if len(state.RoomAgents.AgentIDs) == 0 {
		if c.client != nil && c.client.cfg().RoomFeatures != nil {
			return c.client.cfg().RoomFeatures
		}
		return defaultSDKFeatureConfig()
	}
	agents := make([]*Agent, 0, len(state.RoomAgents.AgentIDs))
	for _, agentID := range state.RoomAgents.AgentIDs {
		agent, err := c.resolveAgentByIdentifier(ctx, agentID)
		if err != nil || agent == nil {
			continue
		}
		agents = append(agents, agent)
	}
	if len(agents) == 0 {
		if c.client != nil && c.client.cfg().RoomFeatures != nil {
			return c.client.cfg().RoomFeatures
		}
		return defaultSDKFeatureConfig()
	}
	return computeRoomFeaturesForAgents(agents)
}

func (c *Conversation) conversationStateSpec() ConversationSpec {
	state := c.state()
	spec := ConversationSpec{
		PortalID:             c.ID,
		Kind:                 state.Kind,
		Visibility:           state.Visibility,
		ParentConversationID: state.ParentConversationID,
		ParentEventID:        state.ParentEventID,
		Title:                c.Title,
		PrimaryAgentID:       state.RoomAgents.PrimaryAgentID,
		ArchiveOnCompletion:  state.ArchiveOnCompletion,
	}
	if len(state.Metadata) > 0 {
		spec.Metadata = make(map[string]any, len(state.Metadata))
		for k, v := range state.Metadata {
			spec.Metadata[k] = v
		}
	}
	return spec
}

func (c *Conversation) aiRoomKind() string {
	if c == nil {
		return agentremote.AIRoomKindAgent
	}
	state := c.state()
	if state.Kind == ConversationKindDelegated || strings.TrimSpace(state.ParentConversationID) != "" {
		return "subagent"
	}
	return agentremote.AIRoomKindAgent
}

// Send sends a complete text message.
func (c *Conversation) Send(ctx context.Context, text string) error {
	return c.SendHTML(ctx, text, "")
}

// SendHTML sends a message with both plaintext and HTML body.
func (c *Conversation) SendHTML(ctx context.Context, text, html string) error {
	intent, err := c.getIntent(ctx)
	if err != nil {
		return err
	}
	content := &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    text,
	}
	if html != "" {
		content.Format = event.FormatHTML
		content.FormattedBody = html
	}
	wrappedContent := &event.Content{Parsed: content}
	_, err = intent.SendMessage(ctx, c.portal.MXID, event.EventMessage, wrappedContent, nil)
	return err
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
	case len(mediaType) > 5 && mediaType[:6] == "image/":
		msgType = event.MsgImage
	case len(mediaType) > 5 && mediaType[:6] == "audio/":
		msgType = event.MsgAudio
	case len(mediaType) > 5 && mediaType[:6] == "video/":
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
	intent, err := c.getIntent(ctx)
	if err != nil {
		return err
	}
	content := &event.MessageEventContent{
		MsgType: event.MsgNotice,
		Body:    text,
	}
	wrappedContent := &event.Content{Parsed: content}
	_, err = intent.SendMessage(ctx, c.portal.MXID, event.EventMessage, wrappedContent, nil)
	return err
}

// Stream starts a new streaming response in this conversation.
func (c *Conversation) Stream(ctx context.Context) *Stream {
	return newTurn(ctx, c, nil, nil)
}

// StartTurn creates a new Turn for this conversation.
func (c *Conversation) StartTurn(ctx context.Context, agent *Agent, source *SourceRef) *Turn {
	return newTurn(ctx, c, agent, source)
}

// StartDefaultTurn creates a new Turn for this conversation with the room's default agent.
func (c *Conversation) StartDefaultTurn(ctx context.Context, source *SourceRef) *Turn {
	agent, _ := c.resolveDefaultAgent(ctx)
	return newTurn(ctx, c, agent, source)
}

// StartTurnWithAgent is kept as a compatibility helper.
func (c *Conversation) StartTurnWithAgent(ctx context.Context, agent *Agent) *Turn {
	return newTurn(ctx, c, agent, nil)
}

// Session returns the session state from the client, if available.
func (c *Conversation) Session() any {
	if c.client == nil {
		return nil
	}
	return c.client.getSession()
}

// Context returns the conversation's context.
func (c *Conversation) Context() context.Context {
	return c.ctx
}

// LoginHandle returns the login-scoped conversation helper.
func (c *Conversation) LoginHandle() *LoginHandle {
	if c == nil {
		return nil
	}
	return newLoginHandle(c.login, c.client)
}

// Spec returns the current persisted conversation spec snapshot.
func (c *Conversation) Spec() ConversationSpec {
	return c.conversationStateSpec()
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
	if strings.TrimSpace(state.RoomAgents.PrimaryAgentID) == "" {
		state.RoomAgents.PrimaryAgentID = agent.ID
	}
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
			state.RoomAgents.PrimaryAgentID = defaultAgent.ID
			_ = c.saveState(ctx, state)
		}
	}
	result := state.RoomAgents
	result.AgentIDs = append([]string(nil), result.AgentIDs...)
	return &result, nil
}

// SetPrimaryAgent updates the room's default agent.
func (c *Conversation) SetPrimaryAgent(ctx context.Context, agentID string) error {
	state := c.state()
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		state.RoomAgents.PrimaryAgentID = ""
	} else {
		found := false
		for _, existing := range state.RoomAgents.AgentIDs {
			if existing == agentID {
				found = true
				break
			}
		}
		if !found {
			state.RoomAgents.AgentIDs = append(state.RoomAgents.AgentIDs, agentID)
			state.RoomAgents.AgentIDs = normalizeAgentIDs(state.RoomAgents.AgentIDs)
		}
		state.RoomAgents.PrimaryAgentID = agentID
	}
	if err := c.saveState(ctx, state); err != nil {
		return err
	}
	if c.portal != nil && c.login != nil {
		c.portal.UpdateCapabilities(ctx, c.login, false)
	}
	return nil
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
	intent, err := c.getIntent(ctx)
	if err != nil {
		return err
	}
	content := &event.Content{Parsed: &event.RoomNameEventContent{Name: name}}
	_, err = intent.SendState(ctx, c.portal.MXID, event.StateRoomName, "", content, time.Time{})
	return err
}

// SetRoomTopic sets the room topic.
func (c *Conversation) SetRoomTopic(ctx context.Context, topic string) error {
	intent, err := c.getIntent(ctx)
	if err != nil {
		return err
	}
	content := &event.Content{Parsed: &event.TopicEventContent{Topic: topic}}
	_, err = intent.SendState(ctx, c.portal.MXID, event.StateTopic, "", content, time.Time{})
	return err
}

func (c *Conversation) broadcastCapabilities(ctx context.Context, features *RoomFeatures) error {
	if features == nil {
		return nil
	}
	intent, err := c.getIntent(ctx)
	if err != nil {
		return err
	}
	rf := convertRoomFeatures(features)
	content := &event.Content{Parsed: rf}
	_, err = intent.SendState(ctx, c.portal.MXID, event.StateBeeperRoomFeatures, "", content, time.Time{})
	return err
}

// BroadcastCapabilities computes and sends room capability state events.
func (c *Conversation) BroadcastCapabilities(ctx context.Context) error {
	return c.broadcastCapabilities(ctx, c.currentRoomFeatures(ctx))
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

// Intent returns the Matrix API intent for sending events.
func (c *Conversation) Intent(ctx context.Context) (bridgev2.MatrixAPI, error) {
	return c.getIntent(ctx)
}

func normalizeConversationSpec(spec ConversationSpec) ConversationSpec {
	if spec.Kind == ConversationKindDelegated && spec.Visibility == "" {
		spec.Visibility = ConversationVisibilityHidden
	}
	if spec.Kind == "" {
		spec.Kind = ConversationKindNormal
	}
	if spec.Visibility == "" {
		spec.Visibility = ConversationVisibilityNormal
	}
	if spec.Kind == ConversationKindDelegated && !spec.ArchiveOnCompletion {
		spec.ArchiveOnCompletion = true
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
		RoomAgents: RoomAgentSet{
			PrimaryAgentID: strings.TrimSpace(spec.PrimaryAgentID),
		},
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
