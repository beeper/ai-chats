package sdk

import "maunium.net/go/mautrix/event"

var MediaMessageTypes = []event.MessageType{
	event.MsgImage,
	event.MsgVideo,
	event.MsgAudio,
	event.MsgFile,
	event.CapMsgVoice,
	event.CapMsgGIF,
	event.CapMsgSticker,
}

type RoomFeaturesParams struct {
	ID                  string
	File                event.FileFeatureMap
	MaxTextLength       int
	Reply               event.CapabilitySupportLevel
	Thread              event.CapabilitySupportLevel
	Edit                event.CapabilitySupportLevel
	Delete              event.CapabilitySupportLevel
	Reaction            event.CapabilitySupportLevel
	ReadReceipts        bool
	TypingNotifications bool
	DeleteChat          bool
}

func BuildRoomFeatures(p RoomFeaturesParams) *event.RoomFeatures {
	return &event.RoomFeatures{
		ID:                  p.ID,
		File:                p.File,
		MaxTextLength:       p.MaxTextLength,
		Reply:               p.Reply,
		Thread:              p.Thread,
		Edit:                p.Edit,
		Delete:              p.Delete,
		Reaction:            p.Reaction,
		ReadReceipts:        p.ReadReceipts,
		TypingNotifications: p.TypingNotifications,
		DeleteChat:          p.DeleteChat,
	}
}

func BuildMediaFileFeatureMap(build func() *event.FileFeatures) event.FileFeatureMap {
	files := make(event.FileFeatureMap, len(MediaMessageTypes))
	for _, msgType := range MediaMessageTypes {
		files[msgType] = build()
	}
	return files
}
