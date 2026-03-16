package agentremote

import (
	"context"
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
		err = errors.New(coalesceStrings(message, "message send failed"))
	}
	st := bridgev2.WrapErrorInStatus(err).WithSendNotice(true)
	if statusForError != nil {
		st = st.WithStatus(statusForError(err))
	}
	if reason != "" {
		st = st.WithErrorReason(reason)
	} else if reasonForError != nil {
		st = st.WithErrorReason(reasonForError(err))
	}
	if message != "" {
		st = st.WithMessage(message)
	} else {
		st = st.WithErrorAsMessage()
	}
	return st
}

func SendMatrixMessageStatus(
	ctx context.Context,
	portal *bridgev2.Portal,
	evt *event.Event,
	status bridgev2.MessageStatus,
) {
	if portal == nil || portal.Bridge == nil || evt == nil {
		return
	}
	portal.Bridge.Matrix.SendMessageStatus(ctx, &status, bridgev2.StatusEventInfoFromEvent(evt))
}
