package turns

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/id"
)

// StreamTarget identifies a bridgev2 message target using bridge-side message
// identity. Matrix event IDs are resolved from bridge DB rows when needed for
// Matrix-native relations.
type StreamTarget struct {
	NetworkMessageID networkid.MessageID
	PartID           networkid.PartID
}

func (t StreamTarget) HasEditTarget() bool {
	return t.NetworkMessageID != ""
}

type TargetEventResolver func(ctx context.Context, target StreamTarget) (id.EventID, error)

func ResolveTargetEventIDFromDB(
	ctx context.Context,
	bridge *bridgev2.Bridge,
	receiver networkid.UserLoginID,
	target StreamTarget,
) (id.EventID, error) {
	if bridge == nil || bridge.DB == nil || !target.HasEditTarget() {
		return "", nil
	}
	var (
		part *database.Message
		err  error
	)
	if target.PartID != "" {
		part, err = bridge.DB.Message.GetPartByID(ctx, receiver, target.NetworkMessageID, target.PartID)
	} else {
		part, err = bridge.DB.Message.GetFirstPartByID(ctx, receiver, target.NetworkMessageID)
	}
	if err != nil || part == nil {
		return "", err
	}
	return part.MXID, nil
}
