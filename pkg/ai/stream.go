package ai

import (
	"context"
	"fmt"
)

func Stream(model Model, c Context, options *StreamOptions) (*AssistantMessageEventStream, error) {
	provider, err := ResolveAPIProvider(model.API)
	if err != nil {
		return nil, err
	}
	if provider.Stream == nil {
		return nil, fmt.Errorf("provider %s has no stream function", model.API)
	}
	return provider.Stream(model, c, options), nil
}

func Complete(model Model, c Context, options *StreamOptions) (Message, error) {
	s, err := Stream(model, c, options)
	if err != nil {
		return Message{}, err
	}
	for {
		_, nextErr := s.Next(context.Background())
		if nextErr != nil {
			break
		}
	}
	return s.Result()
}

func StreamSimple(model Model, c Context, options *SimpleStreamOptions) (*AssistantMessageEventStream, error) {
	provider, err := ResolveAPIProvider(model.API)
	if err != nil {
		return nil, err
	}
	if provider.StreamSimple == nil {
		return nil, fmt.Errorf("provider %s has no streamSimple function", model.API)
	}
	return provider.StreamSimple(model, c, options), nil
}

func CompleteSimple(model Model, c Context, options *SimpleStreamOptions) (Message, error) {
	s, err := StreamSimple(model, c, options)
	if err != nil {
		return Message{}, err
	}
	for {
		_, nextErr := s.Next(context.Background())
		if nextErr != nil {
			break
		}
	}
	return s.Result()
}
