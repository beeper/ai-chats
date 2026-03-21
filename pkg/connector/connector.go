package connector

import ai "github.com/beeper/agentremote/bridges/ai"

type (
	Config          = ai.Config
	OpenAIConnector = ai.OpenAIConnector
)

func NewAIConnector() *OpenAIConnector {
	return ai.NewAIConnector()
}
