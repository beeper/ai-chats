package ai

import (
	"context"
	"errors"

	"maunium.net/go/mautrix/bridgev2"
)

func (oc *AIClient) HandleMatrixRoomName(ctx context.Context, msg *bridgev2.MatrixRoomName) (bool, error) {
	if err := validateRoomMetaMessage(msg != nil && msg.Portal != nil && msg.Content != nil, "room name"); err != nil {
		return false, err
	}
	msg.Portal.Name = msg.Content.Name
	msg.Portal.NameSet = true
	return true, nil
}

func (oc *AIClient) HandleMatrixRoomTopic(ctx context.Context, msg *bridgev2.MatrixRoomTopic) (bool, error) {
	if err := validateRoomMetaMessage(msg != nil && msg.Portal != nil && msg.Content != nil, "room topic"); err != nil {
		return false, err
	}
	msg.Portal.Topic = msg.Content.Topic
	msg.Portal.TopicSet = true
	return true, nil
}

func (oc *AIClient) HandleMatrixRoomAvatar(ctx context.Context, msg *bridgev2.MatrixRoomAvatar) (bool, error) {
	if err := validateRoomMetaMessage(msg != nil && msg.Portal != nil && msg.Content != nil, "room avatar"); err != nil {
		return false, err
	}
	msg.Portal.AvatarID = ""
	msg.Portal.AvatarHash = [32]byte{}
	msg.Portal.AvatarMXC = msg.Content.URL
	msg.Portal.AvatarSet = true
	return true, nil
}

func validateRoomMetaMessage(ok bool, kind string) error {
	if ok {
		return nil
	}
	return errors.New("missing " + kind + " context")
}
