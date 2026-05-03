package connector

import (
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func NewAIConnector() *OpenAIConnector {
	return &OpenAIConnector{
		clients: make(map[networkid.UserLoginID]bridgev2.NetworkAPI),
	}
}
