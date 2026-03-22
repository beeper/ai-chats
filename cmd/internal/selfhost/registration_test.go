package selfhost

import (
	"context"
	"strings"
	"testing"

	"github.com/beeper/bridge-manager/api/beeperapi"
	"github.com/beeper/bridge-manager/api/hungryapi"

	"github.com/beeper/agentremote/cmd/internal/beeperauth"
)

func TestRemoteBridgeDeletedFailsClosedWithoutUsername(t *testing.T) {
	oldWhoami := beeperWhoami
	oldHungryNewClient := hungryNewClient
	t.Cleanup(func() {
		beeperWhoami = oldWhoami
		hungryNewClient = oldHungryNewClient
	})

	beeperWhoami = func(string, string) (*beeperapi.RespWhoami, error) {
		return nil, nil
	}
	hungryNewClient = func(string, string, string) *hungryapi.Client {
		panic("hungry client should not be constructed when username is unavailable")
	}

	_, _, err := remoteBridgeDeleted(context.Background(), beeperauth.Config{
		Domain: "example.com",
		Token:  "token",
	}, "demo")
	if err == nil {
		t.Fatal("expected an error when username is unavailable")
	}
	if !strings.Contains(err.Error(), "beeperWhoami returned nil") {
		t.Fatalf("error %q does not mention unverifiable bridge deletion", err.Error())
	}
}
