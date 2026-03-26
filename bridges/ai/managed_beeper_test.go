package ai

import (
	"testing"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/id"
)

func TestSelectPreferredUserLoginPrefersDefaultMagicProxy(t *testing.T) {
	magic := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       providerLoginID(ProviderMagicProxy, id.UserID("@user:beeper.com"), 1),
			Metadata: &UserLoginMetadata{Provider: ProviderMagicProxy, BaseURL: "https://temporary-ai-proxy.beeper-tools.com", APIKey: "magic-key"},
		},
	}
	otherMagic := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       providerLoginID(ProviderMagicProxy, id.UserID("@user:beeper.com"), 2),
			Metadata: &UserLoginMetadata{Provider: ProviderMagicProxy, BaseURL: "https://temporary-ai-proxy-2.beeper-tools.com", APIKey: "magic-key-2"},
		},
	}

	selected := selectPreferredUserLogin(
		magic,
		[]*bridgev2.UserLogin{magic, otherMagic},
		func(login *bridgev2.UserLogin) bool { return true },
	)
	if selected != magic {
		t.Fatalf("expected default magic proxy login, got %#v", selected)
	}
}

func TestSelectPreferredUserLoginFallsBackToAnotherMagicProxy(t *testing.T) {
	defaultMagic := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       providerLoginID(ProviderMagicProxy, id.UserID("@user:beeper.com"), 1),
			Metadata: &UserLoginMetadata{Provider: ProviderMagicProxy, BaseURL: "https://temporary-ai-proxy.beeper-tools.com/openrouter/v1", APIKey: "magic-key"},
		},
	}
	otherMagic := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       providerLoginID(ProviderMagicProxy, id.UserID("@user:beeper.com"), 2),
			Metadata: &UserLoginMetadata{Provider: ProviderMagicProxy, BaseURL: "https://temporary-ai-proxy-2.beeper-tools.com/openrouter/v1", APIKey: "magic-key-2"},
		},
	}

	selected := selectPreferredUserLogin(
		defaultMagic,
		[]*bridgev2.UserLogin{defaultMagic, otherMagic},
		func(login *bridgev2.UserLogin) bool { return login == otherMagic },
	)
	if selected != otherMagic {
		t.Fatalf("expected fallback magic proxy login, got %#v", selected)
	}
}

func TestSelectPreferredUserLoginReturnsNilWithoutMagicProxy(t *testing.T) {
	openAI := &bridgev2.UserLogin{
		UserLogin: &database.UserLogin{
			ID:       providerLoginID(ProviderOpenAI, id.UserID("@user:beeper.com"), 1),
			Metadata: &UserLoginMetadata{Provider: ProviderOpenAI, APIKey: "openai-key"},
		},
	}

	selected := selectPreferredUserLogin(
		openAI,
		[]*bridgev2.UserLogin{openAI},
		func(login *bridgev2.UserLogin) bool { return false },
	)
	if selected != nil {
		t.Fatalf("expected no preferred login without selectable magic proxy, got %#v", selected)
	}
}
