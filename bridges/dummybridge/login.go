package dummybridge

import (
	"context"
	"fmt"
	"strings"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote"
)

const dummyBridgeLoginStepInput = "io.ai-bridge.dummybridge.enter_value"

var (
	_ bridgev2.LoginProcess          = (*DummyBridgeLogin)(nil)
	_ bridgev2.LoginProcessUserInput = (*DummyBridgeLogin)(nil)
)

type DummyBridgeLogin struct {
	agentremote.BaseLoginProcess
	User      *bridgev2.User
	Connector *DummyBridgeConnector
}

func (dl *DummyBridgeLogin) validate() error {
	var br *bridgev2.Bridge
	if dl.Connector != nil {
		br = dl.Connector.br
	}
	return agentremote.ValidateLoginState(dl.User, br)
}

func (dl *DummyBridgeLogin) Start(_ context.Context) (*bridgev2.LoginStep, error) {
	if err := dl.validate(); err != nil {
		return nil, err
	}
	return &bridgev2.LoginStep{
		Type:         bridgev2.LoginStepTypeUserInput,
		StepID:       dummyBridgeLoginStepInput,
		Instructions: "Enter any string. DummyBridge accepts everything and uses it only for display/debugging.",
		UserInputParams: &bridgev2.LoginUserInputParams{
			Fields: []bridgev2.LoginInputDataField{{
				Type:        bridgev2.LoginInputFieldTypeUsername,
				ID:          "value",
				Name:        "Demo Value",
				Description: "Any text is accepted.",
			}},
		},
	}, nil
}

func (dl *DummyBridgeLogin) SubmitUserInput(ctx context.Context, input map[string]string) (*bridgev2.LoginStep, error) {
	if err := dl.validate(); err != nil {
		return nil, err
	}
	value := input["value"]
	remoteName := dummyAgentName
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		if len(trimmed) > 40 {
			trimmed = trimmed[:40]
		}
		remoteName = fmt.Sprintf("%s (%s)", dummyAgentName, trimmed)
	}
	_, step, err := agentremote.CreateAndCompleteLogin(
		ctx,
		dl.BackgroundProcessContext(),
		dl.User,
		ProviderDummyBridge,
		remoteName,
		&UserLoginMetadata{
			Provider:       ProviderDummyBridge,
			AcceptedString: value,
		},
		"io.ai-bridge.dummybridge.complete",
		dl.Connector.LoadUserLogin,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create dummybridge login: %w", err)
	}
	return step, nil
}
