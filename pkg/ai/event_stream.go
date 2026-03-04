package ai

import (
	"context"
	"errors"
	"io"
	"sync"
)

var ErrStreamClosed = errors.New("assistant message event stream closed")

type AssistantMessageEventStream struct {
	ch        chan AssistantMessageEvent
	done      chan struct{}
	once      sync.Once
	mu        sync.Mutex
	result    Message
	hasResult bool
}

func NewAssistantMessageEventStream(buffer int) *AssistantMessageEventStream {
	if buffer <= 0 {
		buffer = 32
	}
	return &AssistantMessageEventStream{
		ch:   make(chan AssistantMessageEvent, buffer),
		done: make(chan struct{}),
	}
}

func (s *AssistantMessageEventStream) Push(evt AssistantMessageEvent) {
	select {
	case <-s.done:
		return
	default:
	}

	isComplete := false
	if evt.Type == EventDone {
		s.mu.Lock()
		s.result = evt.Message
		s.hasResult = true
		s.mu.Unlock()
		isComplete = true
	}
	if evt.Type == EventError {
		s.mu.Lock()
		s.result = evt.Error
		s.hasResult = true
		s.mu.Unlock()
		isComplete = true
	}

	select {
	case <-s.done:
	case s.ch <- evt:
	}
	if isComplete {
		s.Close()
	}
}

func (s *AssistantMessageEventStream) Close() {
	s.once.Do(func() {
		close(s.done)
		close(s.ch)
	})
}

func (s *AssistantMessageEventStream) Next(ctx context.Context) (AssistantMessageEvent, error) {
	select {
	case <-ctx.Done():
		return AssistantMessageEvent{}, ctx.Err()
	case evt, ok := <-s.ch:
		if !ok {
			return AssistantMessageEvent{}, io.EOF
		}
		return evt, nil
	}
}

func (s *AssistantMessageEventStream) Result() (Message, error) {
	<-s.done
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.hasResult {
		return Message{}, ErrStreamClosed
	}
	return s.result, nil
}
