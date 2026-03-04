package bridgeadapter

import (
	"errors"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

func UnsupportedMessageStatus(err error) error {
	return bridgev2.WrapErrorInStatus(err).
		WithStatus(event.MessageStatusFail).
		WithErrorReason(event.MessageStatusUnsupported).
		WithIsCertain(true).
		WithSendNotice(true).
		WithErrorAsMessage()
}

func MessageSendStatusError(
	err error,
	message string,
	reason event.MessageStatusReason,
	statusForError func(error) event.MessageStatus,
	reasonForError func(error) event.MessageStatusReason,
) error {
	if err == nil {
		msg := message
		if msg == "" {
			msg = "message send failed"
		}
		err = errors.New(msg)
	}
	st := bridgev2.WrapErrorInStatus(err).WithSendNotice(true)
	if statusForError != nil {
		st = st.WithStatus(statusForError(err))
	}
	switch {
	case reason != "":
		st = st.WithErrorReason(reason)
	case reasonForError != nil:
		st = st.WithErrorReason(reasonForError(err))
	}
	if message != "" {
		st = st.WithMessage(message)
	} else {
		st = st.WithErrorAsMessage()
	}
	return st
}
