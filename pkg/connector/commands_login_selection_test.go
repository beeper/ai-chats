package connector

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func TestResolveLoginForCommand_PrefersPortalReceiver(t *testing.T) {
	ctx := context.Background()

	defaultLogin := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: networkid.UserLoginID("default")}}
	receiverLogin := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: networkid.UserLoginID("receiver")}}
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{Receiver: receiverLogin.ID}}}

	got := resolveLoginForCommand(ctx, portal, defaultLogin, func(_ context.Context, id networkid.UserLoginID) (*bridgev2.UserLogin, error) {
		if id != receiverLogin.ID {
			return nil, fmt.Errorf("unexpected lookup id: %s", id)
		}
		return receiverLogin, nil
	})
	if got != receiverLogin {
		t.Fatalf("expected receiver login, got %+v", got)
	}
}

func TestResolveLoginForCommand_FallsBackToDefaultWhenNoReceiver(t *testing.T) {
	ctx := context.Background()

	defaultLogin := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: networkid.UserLoginID("default")}}
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{Receiver: ""}}}

	got := resolveLoginForCommand(ctx, portal, defaultLogin, func(context.Context, networkid.UserLoginID) (*bridgev2.UserLogin, error) {
		t.Fatal("expected lookup not to be called")
		return nil, nil
	})
	if got != defaultLogin {
		t.Fatalf("expected default login, got %+v", got)
	}
}

func TestResolveLoginForCommand_FallsBackToDefaultOnLookupError(t *testing.T) {
	ctx := context.Background()

	defaultLogin := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: networkid.UserLoginID("default")}}
	portal := &bridgev2.Portal{Portal: &database.Portal{PortalKey: networkid.PortalKey{Receiver: networkid.UserLoginID("receiver")}}}

	got := resolveLoginForCommand(ctx, portal, defaultLogin, func(context.Context, networkid.UserLoginID) (*bridgev2.UserLogin, error) {
		return nil, errors.New("boom")
	})
	if got != defaultLogin {
		t.Fatalf("expected default login, got %+v", got)
	}
}

func TestResolveLoginForCommand_FallsBackToDefaultWhenPortalIsNil(t *testing.T) {
	ctx := context.Background()

	defaultLogin := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: networkid.UserLoginID("default")}}

	got := resolveLoginForCommand(ctx, nil, defaultLogin, func(context.Context, networkid.UserLoginID) (*bridgev2.UserLogin, error) {
		t.Fatal("expected lookup not to be called")
		return nil, nil
	})
	if got != defaultLogin {
		t.Fatalf("expected default login, got %+v", got)
	}
}

func TestResolveLoginForCommand_FallsBackToDefaultWhenPortalDataIsNil(t *testing.T) {
	ctx := context.Background()

	defaultLogin := &bridgev2.UserLogin{UserLogin: &database.UserLogin{ID: networkid.UserLoginID("default")}}
	portal := &bridgev2.Portal{Portal: nil}

	got := resolveLoginForCommand(ctx, portal, defaultLogin, func(context.Context, networkid.UserLoginID) (*bridgev2.UserLogin, error) {
		t.Fatal("expected lookup not to be called")
		return nil, nil
	})
	if got != defaultLogin {
		t.Fatalf("expected default login, got %+v", got)
	}
}
