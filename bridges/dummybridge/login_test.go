package dummybridge

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
)

func TestDummyBridgeLoginStartRequestsSingleArbitraryField(t *testing.T) {
	login := &DummyBridgeLogin{
		User:      &bridgev2.User{},
		Connector: &DummyBridgeConnector{br: &bridgev2.Bridge{}},
	}
	step, err := login.Start(context.Background())
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
	if step.Type != bridgev2.LoginStepTypeUserInput {
		t.Fatalf("expected user input step, got %q", step.Type)
	}
	if step.UserInputParams == nil || len(step.UserInputParams.Fields) != 1 {
		t.Fatalf("expected exactly one input field, got %#v", step.UserInputParams)
	}
	field := step.UserInputParams.Fields[0]
	if field.ID != "value" {
		t.Fatalf("expected field id value, got %q", field.ID)
	}
	if field.Type != bridgev2.LoginInputFieldTypeUsername {
		t.Fatalf("expected username field type, got %q", field.Type)
	}
}
