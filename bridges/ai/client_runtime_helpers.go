package ai

import "github.com/rs/zerolog"

func (oc *AIClient) Log() *zerolog.Logger {
	if oc == nil {
		logger := zerolog.Nop()
		return &logger
	}
	return &oc.log
}
