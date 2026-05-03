package ai

import (
	"context"
	"strings"
	"time"

	"go.mau.fi/util/jsontime"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

var rejectAllMediaFileFeatures = &event.FileFeatures{
	MimeTypes: map[string]event.CapabilitySupportLevel{
		"*/*": event.CapLevelRejected,
	},
	Caption: event.CapLevelRejected,
}

func cloneRejectAllMediaFeatures() *event.FileFeatures {
	return rejectAllMediaFileFeatures.Clone()
}

// AI Chats capability constants
const (
	AIMaxTextLength        = 100000
	AIEditMaxAge           = 24 * time.Hour
	modelValidationTimeout = 5 * time.Second
)

const aiCapabilitiesID = "com.beeper.ai.capabilities.2026_02_05"

// aiBaseCaps defines the base capabilities for AI chat rooms
var aiBaseCaps = &event.RoomFeatures{
	ID: aiCapabilitiesID,
	Formatting: map[event.FormattingFeature]event.CapabilitySupportLevel{
		event.FmtBold:          event.CapLevelFullySupported,
		event.FmtItalic:        event.CapLevelFullySupported,
		event.FmtStrikethrough: event.CapLevelFullySupported,
		event.FmtInlineCode:    event.CapLevelFullySupported,
		event.FmtCodeBlock:     event.CapLevelFullySupported,
		event.FmtBlockquote:    event.CapLevelFullySupported,
		event.FmtUnorderedList: event.CapLevelFullySupported,
		event.FmtOrderedList:   event.CapLevelFullySupported,
		event.FmtInlineLink:    event.CapLevelFullySupported,
	},
	File: event.FileFeatureMap{
		event.MsgVideo:      cloneRejectAllMediaFeatures(),
		event.MsgAudio:      cloneRejectAllMediaFeatures(),
		event.MsgFile:       cloneRejectAllMediaFeatures(),
		event.CapMsgVoice:   cloneRejectAllMediaFeatures(),
		event.CapMsgGIF:     cloneRejectAllMediaFeatures(),
		event.CapMsgSticker: cloneRejectAllMediaFeatures(),
		event.MsgImage:      cloneRejectAllMediaFeatures(),
	},
	MaxTextLength:       AIMaxTextLength,
	LocationMessage:     event.CapLevelRejected,
	Poll:                event.CapLevelRejected,
	Reply:               event.CapLevelFullySupported,
	Thread:              event.CapLevelFullySupported,
	Edit:                event.CapLevelFullySupported,
	EditMaxCount:        10,
	EditMaxAge:          ptr.Ptr(jsontime.S(AIEditMaxAge)),
	Delete:              event.CapLevelPartialSupport,
	DeleteMaxAge:        ptr.Ptr(jsontime.S(24 * time.Hour)),
	Reaction:            event.CapLevelFullySupported,
	ReactionCount:       1,
	ReadReceipts:        true,
	TypingNotifications: true,
	Archive:             true,
	MarkAsUnread:        true,
	DeleteChat:          true,
	DisappearingTimer: &event.DisappearingTimerCapability{
		Types: []event.DisappearingType{event.DisappearingTypeAfterSend},
		Timers: []jsontime.Milliseconds{
			jsontime.MS(1 * time.Hour),
			jsontime.MS(24 * time.Hour),
			jsontime.MS(7 * 24 * time.Hour),
			jsontime.MS(90 * 24 * time.Hour),
		},
	},
}

type capabilityIDOptions struct {
	SupportsPDF        bool
	SupportsTextFiles  bool
	SupportsMsgActions bool
}

// buildCapabilityID constructs a deterministic capability ID based on model modalities
// and effective room file capabilities. Suffixes are sorted alphabetically to ensure
// the same capabilities produce the same ID.
func buildCapabilityID(caps ModelCapabilities, opts capabilityIDOptions) string {
	var suffixes []string

	// Add suffixes in alphabetical order for determinism
	if caps.SupportsAudio {
		suffixes = append(suffixes, "audio")
	}
	if caps.SupportsImageGen {
		suffixes = append(suffixes, "imagegen")
	}
	if opts.SupportsMsgActions {
		suffixes = append(suffixes, "msgactions")
	}
	if opts.SupportsPDF || caps.SupportsPDF {
		suffixes = append(suffixes, "pdf")
	}
	if opts.SupportsTextFiles {
		suffixes = append(suffixes, "textfiles")
	}
	if caps.SupportsVideo {
		suffixes = append(suffixes, "video")
	}
	if caps.SupportsVision {
		suffixes = append(suffixes, "vision")
	}

	if len(suffixes) == 0 {
		return aiCapabilitiesID
	}
	return aiCapabilitiesID + "+" + strings.Join(suffixes, "+")
}

// visionFileFeatures returns FileFeatures for vision-capable models
func visionFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"image/png":  event.CapLevelFullySupported,
			"image/jpeg": event.CapLevelFullySupported,
			"image/webp": event.CapLevelFullySupported,
			"image/gif":  event.CapLevelFullySupported,
		},
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          20 * 1024 * 1024, // 20MB
	}
}

func gifFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"image/gif": event.CapLevelFullySupported,
			"video/mp4": event.CapLevelFullySupported,
		},
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          20 * 1024 * 1024, // 20MB
	}
}

func stickerFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"image/webp": event.CapLevelFullySupported,
			"image/png":  event.CapLevelFullySupported,
			"image/gif":  event.CapLevelFullySupported,
		},
		Caption: event.CapLevelDropped,
		MaxSize: 20 * 1024 * 1024, // 20MB
	}
}

// pdfFileFeatures returns FileFeatures for PDF-capable models
func pdfFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"application/pdf": event.CapLevelFullySupported,
		},
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          50 * 1024 * 1024, // 50MB for PDFs
	}
}

func textFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes:        map[string]event.CapabilitySupportLevel{},
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          50 * 1024 * 1024, // Shared cap with PDFs
	}
}

// audioFileFeatures returns FileFeatures for audio-capable models
func audioFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"audio/wav":              event.CapLevelFullySupported,
			"audio/x-wav":            event.CapLevelFullySupported,
			"audio/mpeg":             event.CapLevelFullySupported, // mp3
			"audio/mp3":              event.CapLevelFullySupported,
			"audio/webm":             event.CapLevelFullySupported,
			"audio/ogg":              event.CapLevelFullySupported,
			"audio/ogg; codecs=opus": event.CapLevelFullySupported,
			"audio/flac":             event.CapLevelFullySupported,
			"audio/mp4":              event.CapLevelFullySupported, // m4a
			"audio/x-m4a":            event.CapLevelFullySupported,
		},
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          25 * 1024 * 1024, // 25MB for audio
	}
}

// videoFileFeatures returns FileFeatures for video-capable models
func videoFileFeatures() *event.FileFeatures {
	return &event.FileFeatures{
		MimeTypes: map[string]event.CapabilitySupportLevel{
			"video/mp4":       event.CapLevelFullySupported,
			"video/webm":      event.CapLevelFullySupported,
			"video/mpeg":      event.CapLevelFullySupported,
			"video/quicktime": event.CapLevelFullySupported, // mov
			"video/x-msvideo": event.CapLevelFullySupported, // avi
		},
		Caption:          event.CapLevelFullySupported,
		MaxCaptionLength: AIMaxTextLength,
		MaxSize:          100 * 1024 * 1024, // 100MB for video
	}
}

func (oc *AIClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	meta := portalMeta(portal)
	isModelRoom := meta != nil && meta.ResolvedTarget != nil && meta.ResolvedTarget.Kind == ResolvedTargetModel

	// Always recompute effective room capabilities from the resolved room target.
	modelCaps := oc.getRoomCapabilities(ctx, meta)
	allowTextFiles := oc.canUseMediaUnderstanding(meta)
	supportsPDF := modelCaps.SupportsPDF || oc.isOpenRouterProvider()
	supportsMsgActions := oc.supportsMessageActionsFeature(meta)

	// Clone base capabilities
	caps := aiBaseCaps.Clone()

	// Build dynamic capability ID from modalities
	caps.ID = buildCapabilityID(modelCaps, capabilityIDOptions{
		SupportsPDF:        supportsPDF,
		SupportsTextFiles:  allowTextFiles,
		SupportsMsgActions: supportsMsgActions,
	})

	if supportsMsgActions {
		caps.Reply = event.CapLevelFullySupported
		caps.Reaction = event.CapLevelFullySupported
		caps.ReactionCount = 1
		if isModelRoom {
			caps.Reply = event.CapLevelRejected
			caps.Thread = event.CapLevelRejected
			caps.Edit = event.CapLevelRejected
			caps.EditMaxCount = 0
			caps.EditMaxAge = nil
		} else {
			caps.Edit = event.CapLevelFullySupported
			caps.EditMaxCount = 10
			caps.EditMaxAge = ptr.Ptr(jsontime.S(AIEditMaxAge))
		}
	} else {
		// Use explicit rejected levels so features remain visible in
		// com.beeper.room_features instead of being omitted by omitempty.
		caps.Reply = event.CapLevelRejected
		caps.Edit = event.CapLevelRejected
		caps.EditMaxCount = 0
		caps.EditMaxAge = nil
		caps.Reaction = event.CapLevelRejected
		caps.ReactionCount = 0
	}

	if isModelRoom {
		caps.Reply = event.CapLevelRejected
		caps.Thread = event.CapLevelRejected
	}

	// Apply file capabilities based on modalities
	if modelCaps.SupportsVision {
		caps.File[event.MsgImage] = visionFileFeatures()
		caps.File[event.CapMsgGIF] = gifFileFeatures()
		caps.File[event.CapMsgSticker] = stickerFileFeatures()
	}

	fileFeatures := cloneRejectAllMediaFeatures()
	fileEnabled := false

	// OpenRouter/Beeper: all models support PDF via file-parser plugin
	// For other providers, check model's native PDF support
	if supportsPDF {
		for mime := range pdfFileFeatures().MimeTypes {
			fileFeatures.MimeTypes[mime] = event.CapLevelFullySupported
		}
		fileEnabled = true
	}
	if allowTextFiles {
		for mime := range textFileFeatures().MimeTypes {
			fileFeatures.MimeTypes[mime] = event.CapLevelFullySupported
		}
		fileEnabled = true
	}
	if fileEnabled {
		fileFeatures.Caption = event.CapLevelFullySupported
		fileFeatures.MaxCaptionLength = AIMaxTextLength
		fileFeatures.MaxSize = 50 * 1024 * 1024
		caps.File[event.MsgFile] = fileFeatures
	}

	if modelCaps.SupportsAudio {
		caps.File[event.MsgAudio] = audioFileFeatures()
		// Allow voice notes when audio understanding is available.
		caps.File[event.CapMsgVoice] = audioFileFeatures()
	}
	if modelCaps.SupportsVideo {
		caps.File[event.MsgVideo] = videoFileFeatures()
	}
	// Note: ImageGen is output capability - doesn't affect file upload features
	// Note: Reasoning is processing mode - doesn't affect room features

	return caps
}

func (oc *AIClient) supportsMessageActionsFeature(meta *PortalMetadata) bool {
	if meta == nil {
		return false
	}
	if oc == nil {
		return true
	}
	if oc.connector == nil {
		return true
	}
	return false
}
