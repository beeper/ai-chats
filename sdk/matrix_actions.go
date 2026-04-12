package sdk

import (
	"context"
	"fmt"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote"
)

func resolveMatrixIntent(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	eventType bridgev2.RemoteEventType,
) (bridgev2.MatrixAPI, error) {
	if portal == nil || login == nil {
		return nil, fmt.Errorf("no portal or login")
	}
	intent, ok := portal.GetIntentFor(ctx, sender, login, eventType)
	if !ok || intent == nil {
		return nil, fmt.Errorf("failed to get intent")
	}
	return intent, nil
}

func SendSystemNotice(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	body string,
) error {
	return agentremote.SendSystemMessage(ctx, login, portal, sender, body)
}

func SetRoomName(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	name string,
) error {
	intent, err := resolveMatrixIntent(ctx, login, portal, sender, bridgev2.RemoteEventChatResync)
	if err != nil {
		return err
	}
	_, err = intent.SendState(ctx, portal.MXID, event.StateRoomName, "", &event.Content{
		Parsed: &event.RoomNameEventContent{Name: name},
	}, time.Time{})
	return err
}

func SetRoomTopic(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	topic string,
) error {
	intent, err := resolveMatrixIntent(ctx, login, portal, sender, bridgev2.RemoteEventChatResync)
	if err != nil {
		return err
	}
	_, err = intent.SendState(ctx, portal.MXID, event.StateTopic, "", &event.Content{
		Parsed: &event.TopicEventContent{Topic: topic},
	}, time.Time{})
	return err
}

func BroadcastCapabilities(
	ctx context.Context,
	login *bridgev2.UserLogin,
	portal *bridgev2.Portal,
	sender bridgev2.EventSender,
	features *RoomFeatures,
) error {
	if features == nil {
		return nil
	}
	intent, err := resolveMatrixIntent(ctx, login, portal, sender, bridgev2.RemoteEventChatResync)
	if err != nil {
		return err
	}
	_, err = intent.SendState(ctx, portal.MXID, event.StateBeeperRoomFeatures, "", &event.Content{
		Parsed: convertRoomFeatures(features),
	}, time.Time{})
	return err
}

func SendMessageStatus(
	ctx context.Context,
	portal *bridgev2.Portal,
	roomID id.RoomID,
	sourceEventID id.EventID,
	status event.MessageStatus,
	message string,
) {
	if portal == nil || portal.Bridge == nil || portal.Bridge.Matrix == nil || sourceEventID == "" {
		return
	}
	statusContent := bridgev2.MessageStatus{
		Status:    status,
		Message:   message,
		IsCertain: true,
	}
	portal.Bridge.Matrix.SendMessageStatus(ctx, &statusContent, &bridgev2.MessageStatusEventInfo{
		RoomID:        roomID,
		SourceEventID: sourceEventID,
	})
}

func SendEventMessageStatus(
	ctx context.Context,
	portal *bridgev2.Portal,
	evt *event.Event,
	status bridgev2.MessageStatus,
) {
	agentremote.SendMatrixMessageStatus(ctx, portal, evt, status)
}

func SendAIRoomInfo(ctx context.Context, portal *bridgev2.Portal, aiKind string) bool {
	return agentremote.SendAIRoomInfo(ctx, portal, aiKind)
}
