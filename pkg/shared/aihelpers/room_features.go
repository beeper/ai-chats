package aihelpers

import "maunium.net/go/mautrix/event"

func DefaultRoomFeatures() *RoomFeatures {
	return defaultAIHelperFeatureConfig()
}

func RoomFeaturesToMatrix(features *RoomFeatures) *event.RoomFeatures {
	if features == nil {
		features = defaultAIHelperFeatureConfig()
	}
	maxText := features.MaxTextLength
	if maxText == 0 {
		maxText = DefaultAgentMaxTextLength
	}
	capID := features.CustomCapabilityID
	if capID == "" {
		capID = "com.beeper.ai_chats.helpers"
	}
	roomFeatures := &event.RoomFeatures{
		ID:                  capID,
		MaxTextLength:       maxText,
		Reply:               capLevel(features.SupportsReply),
		Edit:                capLevel(features.SupportsEdit),
		Delete:              capLevel(features.SupportsDelete),
		Reaction:            capLevel(features.SupportsReactions),
		ReadReceipts:        features.SupportsReadReceipts,
		TypingNotifications: features.SupportsTyping,
		DeleteChat:          features.SupportsDeleteChat,
		File:                make(event.FileFeatureMap),
	}
	if features.SupportsImages {
		roomFeatures.File[event.MsgImage] = &event.FileFeatures{}
	}
	if features.SupportsAudio {
		roomFeatures.File[event.MsgAudio] = &event.FileFeatures{}
	}
	if features.SupportsVideo {
		roomFeatures.File[event.MsgVideo] = &event.FileFeatures{}
	}
	if features.SupportsFiles {
		roomFeatures.File[event.MsgFile] = &event.FileFeatures{}
	}
	return roomFeatures
}

func defaultAIHelperFeatureConfig() *RoomFeatures {
	return &RoomFeatures{
		MaxTextLength:        DefaultAgentMaxTextLength,
		SupportsReply:        true,
		SupportsReactions:    true,
		SupportsTyping:       true,
		SupportsReadReceipts: true,
		SupportsDeleteChat:   true,
	}
}

func computeRoomFeaturesForAgents(agents []*Agent) *RoomFeatures {
	if len(agents) == 0 {
		return defaultAIHelperFeatureConfig()
	}

	// Merge capabilities across all agents: any agent supporting a feature enables it.
	var merged AgentCapabilities
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		caps := agent.Capabilities
		if caps.MaxTextLength > merged.MaxTextLength {
			merged.MaxTextLength = caps.MaxTextLength
		}
		merged.SupportsStreaming = merged.SupportsStreaming || caps.SupportsStreaming
		merged.SupportsReasoning = merged.SupportsReasoning || caps.SupportsReasoning
		merged.SupportsToolCalling = merged.SupportsToolCalling || caps.SupportsToolCalling
		merged.SupportsTextInput = merged.SupportsTextInput || caps.SupportsTextInput
		merged.SupportsImageInput = merged.SupportsImageInput || caps.SupportsImageInput
		merged.SupportsAudioInput = merged.SupportsAudioInput || caps.SupportsAudioInput
		merged.SupportsVideoInput = merged.SupportsVideoInput || caps.SupportsVideoInput
		merged.SupportsFileInput = merged.SupportsFileInput || caps.SupportsFileInput
		merged.SupportsPDFInput = merged.SupportsPDFInput || caps.SupportsPDFInput
		merged.SupportsImageOutput = merged.SupportsImageOutput || caps.SupportsImageOutput
		merged.SupportsAudioOutput = merged.SupportsAudioOutput || caps.SupportsAudioOutput
		merged.SupportsFilesOutput = merged.SupportsFilesOutput || caps.SupportsFilesOutput
	}

	base := defaultAIHelperFeatureConfig()
	if merged.MaxTextLength > 0 {
		base.MaxTextLength = merged.MaxTextLength
	}
	base.SupportsImages = merged.SupportsImageInput || merged.SupportsImageOutput
	base.SupportsAudio = merged.SupportsAudioInput || merged.SupportsAudioOutput
	base.SupportsVideo = merged.SupportsVideoInput
	base.SupportsFiles = merged.SupportsFileInput || merged.SupportsPDFInput || merged.SupportsFilesOutput
	base.SupportsReply = merged.SupportsTextInput
	base.SupportsTyping = merged.SupportsStreaming
	base.SupportsReactions = merged.SupportsToolCalling || merged.SupportsReasoning || merged.SupportsTextInput
	base.SupportsReadReceipts = true
	base.SupportsDeleteChat = true
	return base
}

func capLevel(supported bool) event.CapabilitySupportLevel {
	if supported {
		return event.CapLevelFullySupported
	}
	return event.CapLevelRejected
}
