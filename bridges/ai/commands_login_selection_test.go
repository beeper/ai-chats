package ai

import (
	"context"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestResolveLoginForCommand_UsesDefaultWithoutPortal(t *testing.T) {
	ctx := context.Background()
	defaultLogin := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: networkid.UserLoginID("default")}}

	got := resolveLoginForCommand(ctx, nil, nil, defaultLogin, nil)
	if got != defaultLogin {
		t.Fatalf("expected default login, got %+v", got)
	}
}

func TestResolveLoginForCommand_RejectsPortalScopedFallback(t *testing.T) {
	ctx := context.Background()
	defaultLogin := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: networkid.UserLoginID("default")}}
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{Receiver: networkid.UserLoginID("receiver")}}}

	got := resolveLoginForCommand(ctx, portal, nil, defaultLogin, nil)
	if got != nil {
		t.Fatalf("expected nil login for unresolved portal ownership, got %+v", got)
	}
}
