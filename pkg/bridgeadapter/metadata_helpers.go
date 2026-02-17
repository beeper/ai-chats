package bridgeadapter

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
)

func EnsureLoginMetadata[T any](login *bridgev2.UserLogin) *T {
	if login == nil {
		return new(T)
	}
	if login.Metadata == nil {
		meta := new(T)
		login.Metadata = meta
		return meta
	}
	meta, ok := login.Metadata.(*T)
	if !ok || meta == nil {
		meta = new(T)
		login.Metadata = meta
	}
	return meta
}

func EnsurePortalMetadata[T any](portal *bridgev2.Portal) *T {
	if portal == nil {
		return new(T)
	}
	meta, ok := portal.Metadata.(*T)
	if !ok || meta == nil {
		meta = new(T)
		portal.Metadata = meta
	}
	return meta
}

func EnsureMessageMetadata[T any](msg *database.Message) *T {
	if msg == nil {
		return new(T)
	}
	meta, ok := msg.Metadata.(*T)
	if !ok || meta == nil {
		meta = new(T)
		msg.Metadata = meta
	}
	return meta
}
