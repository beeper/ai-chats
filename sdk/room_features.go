package sdk

import "maunium.net/go/mautrix/event"

func defaultSDKFeatureConfig() *RoomFeatures {
	return &RoomFeatures{
		MaxTextLength:        100000,
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
	minText := 0
	allStreaming := true
	allReasoning := true
	allTools := true
	allTextInput := true
	allImageInput := true
	allAudioInput := true
	allVideoInput := true
	allFileInput := true
	allPDFInput := true
	allImageOutput := true
	allAudioOutput := true
	allFilesOutput := true
	for _, agent := range agents {
		if agent == nil {
			continue
		}
		caps := agent.Capabilities
		if minText == 0 || (caps.MaxTextLength > 0 && caps.MaxTextLength < minText) {
			if caps.MaxTextLength > 0 {
				minText = caps.MaxTextLength
			}
		}
		allStreaming = allStreaming && caps.SupportsStreaming
		allReasoning = allReasoning && caps.SupportsReasoning
		allTools = allTools && caps.SupportsToolCalling
		allTextInput = allTextInput && caps.SupportsTextInput
		allImageInput = allImageInput && caps.SupportsImageInput
		allAudioInput = allAudioInput && caps.SupportsAudioInput
		allVideoInput = allVideoInput && caps.SupportsVideoInput
		allFileInput = allFileInput && caps.SupportsFileInput
		allPDFInput = allPDFInput && caps.SupportsPDFInput
		allImageOutput = allImageOutput && caps.SupportsImageOutput
		allAudioOutput = allAudioOutput && caps.SupportsAudioOutput
		allFilesOutput = allFilesOutput && caps.SupportsFilesOutput
	}

	base := defaultSDKFeatureConfig()
	if minText > 0 {
		base.MaxTextLength = minText
	}
	base.SupportsImages = allImageInput || allImageOutput
	base.SupportsAudio = allAudioInput || allAudioOutput
	base.SupportsVideo = allVideoInput
	base.SupportsFiles = allFileInput || allPDFInput || allFilesOutput
	base.SupportsReply = allTextInput
	base.SupportsTyping = allStreaming
	base.SupportsReactions = allTools || allReasoning || allTextInput
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
		maxText = 100000
	}
	capID := f.CustomCapabilityID
	if capID == "" {
		capID = "com.beeper.ai.sdk"
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

func defaultSDKRoomFeatures() *event.RoomFeatures {
	return convertRoomFeatures(defaultSDKFeatureConfig())
}

func capLevel(supported bool) event.CapabilitySupportLevel {
	if supported {
		return event.CapLevelFullySupported
	}
	return event.CapLevelRejected
}
