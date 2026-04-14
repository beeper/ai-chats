package ai

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/id"
)

type deliveryTarget struct {
	Portal  *bridgev2.Portal
	RoomID  id.RoomID
	Channel string
	Reason  string
}

type heartbeatRoute struct {
	Session       heartbeatSessionResolution
	SessionPortal *bridgev2.Portal
	Delivery      deliveryTarget
}
