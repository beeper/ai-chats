package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"
)

func TestRoomCapabilitiesFollowModelModalities(t *testing.T) {
	ctx := context.Background()
	oc := newTestAIClientWithProvider(ProviderOpenRouter)

	textPortal := capabilityTestPortal("google/gemma-2-27b-it")
	textCaps := oc.GetCapabilities(ctx, textPortal)
	if capabilityLevel(textCaps.File[event.MsgImage], "image/png") != event.CapLevelRejected {
		t.Fatalf("text-only model should reject image uploads")
	}
	if capabilityLevel(textCaps.File[event.MsgAudio], "audio/mpeg") != event.CapLevelRejected {
		t.Fatalf("text-only model should reject audio uploads")
	}
	if capabilityLevel(textCaps.File[event.MsgVideo], "video/mp4") != event.CapLevelRejected {
		t.Fatalf("text-only model should reject video uploads")
	}

	mediaPortal := capabilityTestPortal("google/gemini-2.5-pro")
	mediaCaps := oc.GetCapabilities(ctx, mediaPortal)
	if capabilityLevel(mediaCaps.File[event.MsgImage], "image/png") != event.CapLevelFullySupported {
		t.Fatalf("multimodal model should allow image uploads")
	}
	if capabilityLevel(mediaCaps.File[event.MsgAudio], "audio/mpeg") != event.CapLevelFullySupported {
		t.Fatalf("multimodal model should allow audio uploads")
	}
	if capabilityLevel(mediaCaps.File[event.CapMsgVoice], "audio/mpeg") != event.CapLevelFullySupported {
		t.Fatalf("multimodal model should allow voice messages")
	}
	if capabilityLevel(mediaCaps.File[event.MsgVideo], "video/mp4") != event.CapLevelFullySupported {
		t.Fatalf("multimodal model should allow video uploads")
	}
}

func capabilityTestPortal(modelID string) *bridgev2.Portal {
	return &bridgev2.Portal{Portal: &database.Portal{
		OtherUserID: modelUserID(modelID),
		Metadata:    &PortalMetadata{},
	}}
}

func capabilityLevel(features *event.FileFeatures, mime string) event.CapabilitySupportLevel {
	if features == nil {
		return event.CapLevelRejected
	}
	if level, ok := features.MimeTypes[mime]; ok {
		return level
	}
	return features.MimeTypes["*/*"]
}
