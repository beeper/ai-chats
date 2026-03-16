package agentremote

import (
	"maunium.net/go/mautrix/bridgev2"
)

// EnsureMetadata type-asserts or initializes a metadata pointer from a holder.
// holder is the pointer to the Metadata field (e.g. &login.Metadata).
func EnsureMetadata[T any](holder *any) *T {
	if holder == nil {
		return new(T)
	}
	if meta, ok := (*holder).(*T); ok && meta != nil {
		return meta
	}
	meta := new(T)
	*holder = meta
	return meta
}

func EnsureLoginMetadata[T any](login *bridgev2.UserLogin) *T {
	if login == nil {
		return new(T)
	}
	return EnsureMetadata[T](&login.Metadata)
}

func EnsurePortalMetadata[T any](portal *bridgev2.Portal) *T {
	if portal == nil {
		return new(T)
	}
	return EnsureMetadata[T](&portal.Metadata)
}
