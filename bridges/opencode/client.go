package opencode

import (
	"context"
	"errors"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/shared/streamui"
	"github.com/beeper/agentremote/sdk"
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
	sdk.ClientBase
	UserLogin *bridgev2.UserLogin
	connector *OpenCodeConnector
	bridge    *Bridge

	streamHost *sdk.StreamTurnHost[openCodeStreamState]
}

type openCodeStreamState struct {
	portal           *bridgev2.Portal
	turnID           string
	agentID          string
	turn             *sdk.Turn
	stream           sdk.StreamPartState
	ui               streamui.UIState
	role             string
	sessionID        string
	messageID        string
	parentMessageID  string
	agent            string
	modelID          string
	providerID       string
	mode             string
	promptTokens     int64
	completionTokens int64
	reasoningTokens  int64
	totalTokens      int64
	cost             float64
}

func newOpenCodeClient(login *bridgev2.UserLogin, connector *OpenCodeConnector) (*OpenCodeClient, error) {
	if login == nil {
		return nil, errors.New("missing login")
	}
	if connector == nil {
		return nil, errors.New("missing connector")
	}
	client := &OpenCodeClient{
		UserLogin: login,
		connector: connector,
	}
	client.streamHost = sdk.NewStreamTurnHost(sdk.StreamTurnHostCallbacks[openCodeStreamState]{
		GetAborter: func(s *openCodeStreamState) sdk.Aborter {
			if s.turn == nil {
				return nil
			}
			return s.turn
		},
	})
	client.InitClientBase(login, client)
	client.HumanUserIDPrefix = "opencode-user"
	client.MessageIDPrefix = "opencode"
	client.MessageLogKey = "opencode_msg_id"
	client.bridge = NewBridge(client)
	return client, nil
}

func (oc *OpenCodeClient) SetUserLogin(login *bridgev2.UserLogin) {
	oc.UserLogin = login
	oc.ClientBase.SetUserLogin(login)
}

func (oc *OpenCodeClient) Connect(ctx context.Context) {
	oc.ResetStreamShutdown()
	oc.SetLoggedIn(false)
	oc.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnecting, Message: "Connecting"})
	if oc.bridge != nil {
		go func() {
			if err := oc.bridge.RestoreConnections(oc.BackgroundContext(ctx)); err != nil {
				oc.UserLogin.Log.Warn().Err(err).Msg("Failed to restore OpenCode connections")
				oc.UserLogin.BridgeState.Send(status.BridgeState{
					StateEvent: status.StateTransientDisconnect,
					Message:    "Failed to restore OpenCode connections",
				})
				return
			}
			connected := oc.hasReachableOpenCodeInstance()
			if !connected {
				oc.UserLogin.BridgeState.Send(status.BridgeState{
					StateEvent: status.StateTransientDisconnect,
					Message:    "No OpenCode instances are currently reachable",
				})
				return
			}
			oc.SetLoggedIn(true)
			oc.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected, Message: "Connected"})
		}()
		return
	}
	oc.SetLoggedIn(true)
	oc.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateConnected, Message: "Connected"})
}

func (oc *OpenCodeClient) Disconnect() {
	oc.BeginStreamShutdown()
	oc.SetLoggedIn(false)
	oc.CloseAllSessions()
	oc.streamHost.DrainAndAbort("disconnect")
	if oc.bridge != nil && oc.bridge.manager != nil && oc.bridge.manager.approvalFlow != nil {
		oc.bridge.manager.approvalFlow.Close()
	}
	if oc.bridge != nil {
		oc.bridge.DisconnectAll()
	}
	if oc.UserLogin != nil {
		oc.UserLogin.BridgeState.Send(status.BridgeState{StateEvent: status.StateTransientDisconnect, Message: "Disconnected"})
	}
}

func (oc *OpenCodeClient) GetUserLogin() *bridgev2.UserLogin { return oc.UserLogin }

func (oc *OpenCodeClient) hasReachableOpenCodeInstance() bool {
	instances := oc.OpenCodeInstances()
	if len(instances) == 0 {
		return true
	}
	if oc.bridge == nil || oc.bridge.manager == nil {
		return false
	}
	for instanceID := range instances {
		if oc.bridge.manager.IsConnected(instanceID) {
			return true
		}
	}
	return false
}

func (oc *OpenCodeClient) GetApprovalHandler() sdk.ApprovalReactionHandler {
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
	pmeta := oc.PortalMeta(msg.Portal)
	if pmeta == nil || !pmeta.IsOpenCodeRoom {
		return &bridgev2.MatrixMessageResponse{Pending: false}, nil
	}
	return oc.bridge.HandleMatrixMessage(ctx, msg, msg.Portal, pmeta)
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
	if params.Portal == nil || !portalMeta(params.Portal).IsOpenCodeRoom {
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
	return sdk.BuildRoomFeatures(sdk.RoomFeaturesParams{
		ID:                  "com.beeper.ai.capabilities.2026_02_17+opencode",
		File:                sdk.BuildMediaFileFeatureMap(func() *event.FileFeatures { return openCodeFileFeatures }),
		MaxTextLength:       100000,
		Reply:               event.CapLevelFullySupported,
		Thread:              event.CapLevelFullySupported,
		Edit:                event.CapLevelRejected,
		Delete:              event.CapLevelRejected,
		Reaction:            event.CapLevelFullySupported,
		ReadReceipts:        true,
		TypingNotifications: true,
		DeleteChat:          true,
	})
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

func (oc *OpenCodeClient) LogoutRemote(_ context.Context) {
	oc.Disconnect()
	if oc.connector != nil && oc.UserLogin != nil {
		sdk.RemoveClientFromCache(&oc.connector.clientsMu, oc.connector.clients, oc.UserLogin.ID)
	}
}

func (oc *OpenCodeClient) GetChatInfo(_ context.Context, portal *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	if portal == nil {
		return nil, nil
	}
	pmeta := portalMeta(portal)
	if !pmeta.IsOpenCodeRoom {
		return nil, nil
	}
	return sdk.BuildChatInfoWithFallback(pmeta.Title, portal.Name, "OpenCode", portal.Topic), nil
}

func (oc *OpenCodeClient) instanceDisplayName(instanceID string) string {
	if oc != nil && oc.bridge != nil {
		if name := strings.TrimSpace(oc.bridge.DisplayName(instanceID)); name != "" {
			return name
		}
	}
	return "OpenCode"
}

func openCodeSDKAgent(instanceID, displayName string) *sdk.Agent {
	if displayName == "" {
		displayName = "OpenCode"
	}
	return &sdk.Agent{
		ID:           string(OpenCodeUserID(instanceID)),
		Name:         displayName,
		Description:  "OpenCode instance",
		Identifiers:  []string{"opencode:" + instanceID},
		ModelKey:     "opencode:" + instanceID,
		Capabilities: sdk.MultimodalAgentCapabilities(),
	}
}
