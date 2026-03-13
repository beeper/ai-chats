package opencode

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/streamui"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

var (
	_ bridgev2.NetworkAPI                    = (*OpenCodeClient)(nil)
	_ bridgev2.BackfillingNetworkAPI         = (*OpenCodeClient)(nil)
	_ bridgev2.DeleteChatHandlingNetworkAPI  = (*OpenCodeClient)(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI = (*OpenCodeClient)(nil)
	_ bridgev2.ContactListingNetworkAPI      = (*OpenCodeClient)(nil)
	_ bridgev2.UserSearchingNetworkAPI       = (*OpenCodeClient)(nil)
	_ bridgev2.ReactionHandlingNetworkAPI    = (*OpenCodeClient)(nil)
)

type OpenCodeClient struct {
	agentremote.ClientBase
	UserLogin *bridgev2.UserLogin
	connector *OpenCodeConnector
	bridge    *Bridge

	loggedIn atomic.Bool

	streamStates map[string]*openCodeStreamState
}

type openCodeStreamState struct {
	portal               *bridgev2.Portal
	turnID               string
	agentID              string
	turn                 *bridgesdk.Turn
	initialEventID       id.EventID
	networkMessageID     networkid.MessageID
	sequenceNum          int
	lastRemoteEventOrder int64
	accumulated          strings.Builder
	visible              strings.Builder
	ui                   streamui.UIState
	role                 string
	sessionID            string
	messageID            string
	parentMessageID      string
	agent                string
	modelID              string
	providerID           string
	mode                 string
	finishReason         string
	errorText            string
	startedAtMs          int64
	completedAtMs        int64
	promptTokens         int64
	completionTokens     int64
	reasoningTokens      int64
	totalTokens          int64
	cost                 float64
}

func newOpenCodeClient(login *bridgev2.UserLogin, connector *OpenCodeConnector) (*OpenCodeClient, error) {
	if login == nil {
		return nil, errors.New("missing login")
	}
	if connector == nil {
		return nil, errors.New("missing connector")
	}
	client := &OpenCodeClient{
		UserLogin:    login,
		connector:    connector,
		streamStates: make(map[string]*openCodeStreamState),
	}
	client.InitClientBase(login, client)
	client.bridge = NewBridge(client)
	return client, nil
}

func (oc *OpenCodeClient) SetUserLogin(login *bridgev2.UserLogin) {
	oc.UserLogin = login
	oc.ClientBase.SetUserLogin(login)
}

func (oc *OpenCodeClient) Connect(ctx context.Context) {
	oc.ResetStreamShutdown()
	oc.loggedIn.Store(true)
	oc.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected, Message: "Connected"})
	if oc.bridge != nil {
		go func() {
			if err := oc.bridge.RestoreConnections(oc.BackgroundContext(ctx)); err != nil {
				oc.UserLogin.Log.Warn().Err(err).Msg("Failed to restore OpenCode connections")
			}
		}()
	}
}

func (oc *OpenCodeClient) Disconnect() {
	oc.BeginStreamShutdown()
	oc.loggedIn.Store(false)
	oc.CloseAllSessions()
	oc.StreamMu.Lock()
	oc.streamStates = make(map[string]*openCodeStreamState)
	oc.StreamMu.Unlock()
	if oc.bridge != nil {
		oc.bridge.DisconnectAll()
	}
	if oc.UserLogin != nil {
		oc.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateTransientDisconnect, Message: "Disconnected"})
	}
}

func (oc *OpenCodeClient) IsLoggedIn() bool {
	return oc.loggedIn.Load()
}

func (oc *OpenCodeClient) GetUserLogin() *bridgev2.UserLogin { return oc.UserLogin }

func (oc *OpenCodeClient) GetApprovalHandler() agentremote.ApprovalReactionHandler {
	if oc.bridge == nil {
		return nil
	}
	return oc.bridge.ApprovalHandler()
}

func (oc *OpenCodeClient) HandleMatrixMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if msg == nil || msg.Portal == nil {
		return nil, errors.New("missing portal context")
	}
	if oc.bridge == nil {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	meta := portalMeta(msg.Portal)
	if !meta.IsOpenCodeRoom {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	return oc.bridge.HandleMatrixMessage(ctx, msg, msg.Portal, oc.PortalMeta(msg.Portal))
}

func (oc *OpenCodeClient) HandleMatrixDeleteChat(ctx context.Context, msg *bridgev2.MatrixDeleteChat) error {
	if oc.bridge == nil {
		return nil
	}
	return oc.bridge.HandleMatrixDeleteChat(ctx, msg)
}

func (oc *OpenCodeClient) FetchMessages(ctx context.Context, params bridgev2.FetchMessagesParams) (*bridgev2.FetchMessagesResponse, error) {
	if oc.bridge == nil {
		return nil, nil
	}
	if params.Portal == nil {
		return nil, nil
	}
	meta := portalMeta(params.Portal)
	if !meta.IsOpenCodeRoom {
		return nil, nil
	}
	return oc.bridge.FetchMessages(ctx, params)
}

var openCodeFileFeatures = &event.FileFeatures{
	MimeTypes: map[string]event.CapabilitySupportLevel{
		"*/*": event.CapLevelFullySupported,
	},
	Caption:          event.CapLevelFullySupported,
	MaxCaptionLength: 100000,
	MaxSize:          50 * 1024 * 1024,
}

func openCodeMatrixRoomFeatures() *event.RoomFeatures {
	return &event.RoomFeatures{
		ID: "com.beeper.ai.capabilities.2026_02_17+opencode",
		File: event.FileFeatureMap{
			event.MsgImage:      openCodeFileFeatures,
			event.MsgVideo:      openCodeFileFeatures,
			event.MsgAudio:      openCodeFileFeatures,
			event.MsgFile:       openCodeFileFeatures,
			event.CapMsgVoice:   openCodeFileFeatures,
			event.CapMsgGIF:     openCodeFileFeatures,
			event.CapMsgSticker: openCodeFileFeatures,
		},
		MaxTextLength:       100000,
		Reply:               event.CapLevelFullySupported,
		Thread:              event.CapLevelFullySupported,
		Edit:                event.CapLevelRejected,
		Delete:              event.CapLevelRejected,
		Reaction:            event.CapLevelFullySupported,
		ReadReceipts:        true,
		TypingNotifications: true,
		DeleteChat:          true,
	}
}

func (oc *OpenCodeClient) GetCapabilities(_ context.Context, _ *bridgev2.Portal) *event.RoomFeatures {
	return openCodeMatrixRoomFeatures()
}

func (oc *OpenCodeClient) GetUserInfo(_ context.Context, ghost *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	if ghost == nil {
		return openCodeSDKAgent("", "OpenCode").UserInfo(), nil
	}
	instanceID, ok := ParseOpenCodeGhostID(string(ghost.ID))
	if !ok {
		return openCodeSDKAgent("", "OpenCode").UserInfo(), nil
	}
	return openCodeSDKAgent(instanceID, oc.instanceDisplayName(instanceID)).UserInfo(), nil
}

func (oc *OpenCodeClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	return oc.resolveOpenCodeIdentifier(ctx, identifier, createChat)
}

func (oc *OpenCodeClient) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	return oc.openCodeContactList(ctx)
}

func (oc *OpenCodeClient) SearchUsers(ctx context.Context, query string) ([]*bridgev2.ResolveIdentifierResponse, error) {
	return oc.searchOpenCodeUsers(ctx, query)
}

func (oc *OpenCodeClient) LogoutRemote(_ context.Context) {
	oc.Disconnect()
	if oc.connector != nil && oc.UserLogin != nil {
		agentremote.RemoveClientFromCache(&oc.connector.clientsMu, oc.connector.clients, oc.UserLogin.ID)
	}
}

func (oc *OpenCodeClient) IsThisUser(_ context.Context, userID networkid.UserID) bool {
	return userID == humanUserID(oc.UserLogin.ID)
}

func (oc *OpenCodeClient) GetChatInfo(_ context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	if portal == nil {
		return nil, nil
	}
	meta := portalMeta(portal)
	if !meta.IsOpenCodeRoom {
		return nil, nil
	}
	return agentremote.BuildChatInfoWithFallback(meta.Title, portal.Name, "OpenCode", portal.Topic), nil
}
