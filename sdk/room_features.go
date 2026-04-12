package sdk

import "maunium.net/go/mautrix/event"

func defaultSDKFeatureConfig() *RoomFeatures {
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
		return defaultSDKFeatureConfig()
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

	base := defaultSDKFeatureConfig()
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

func convertRoomFeatures(f *RoomFeatures) *event.RoomFeatures {
	if f == nil {
		f = defaultSDKFeatureConfig()
	}
	if f.Custom != nil {
		return f.Custom
	}
	maxText := f.MaxTextLength
	if maxText == 0 {
		maxText = DefaultAgentMaxTextLength
	}
	capID := f.CustomCapabilityID
	if capID == "" {
		capID = "com.beeper.agentremote.sdk"
	}
	rf := &event.RoomFeatures{
		ID:                  capID,
		MaxTextLength:       maxText,
		Reply:               capLevel(f.SupportsReply),
		Edit:                capLevel(f.SupportsEdit),
		Delete:              capLevel(f.SupportsDelete),
		Reaction:            capLevel(f.SupportsReactions),
		ReadReceipts:        f.SupportsReadReceipts,
		TypingNotifications: f.SupportsTyping,
		DeleteChat:          f.SupportsDeleteChat,
		File:                make(event.FileFeatureMap),
	}
	if f.SupportsImages {
		rf.File[event.MsgImage] = &event.FileFeatures{}
	}
	if f.SupportsAudio {
		rf.File[event.MsgAudio] = &event.FileFeatures{}
	}
	if f.SupportsVideo {
		rf.File[event.MsgVideo] = &event.FileFeatures{}
	}
	if f.SupportsFiles {
		rf.File[event.MsgFile] = &event.FileFeatures{}
	}
	return rf
}

func capLevel(supported bool) event.CapabilitySupportLevel {
	if supported {
		return event.CapLevelFullySupported
	}
	return event.CapLevelRejected
}
