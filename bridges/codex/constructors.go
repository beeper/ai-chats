package codex

import "github.com/beeper/agentremote/pkg/bridgeadapter"

func NewConnector() *CodexConnector {
	return &CodexConnector{
		BaseConnectorMethods: bridgeadapter.BaseConnectorMethods{ProtocolID: "ai-codex"},
	}
}
