package connector

import (
	"testing"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

func testAIClientWithMCPServers(servers map[string]MCPServerConfig) *AIClient {
	meta := &UserLoginMetadata{ServiceTokens: &ServiceTokens{MCPServers: servers}}
	login := &database.UserLogin{ID: networkid.UserLoginID("login"), Metadata: meta}
	userLogin := &bridgev2.UserLogin{UserLogin: login, Log: zerolog.Nop()}
	return &AIClient{UserLogin: userLogin}
}

func TestNormalizeMCPServerKind(t *testing.T) {
	if got := normalizeMCPServerKind(""); got != mcpServerKindGeneric {
		t.Fatalf("expected empty kind to default to generic, got %q", got)
	}
	if got := normalizeMCPServerKind("clay"); got != mcpServerKindNexus {
		t.Fatalf("expected clay kind to normalize to nexus, got %q", got)
	}
	if got := normalizeMCPServerKind("nexus"); got != mcpServerKindNexus {
		t.Fatalf("expected nexus kind to remain nexus, got %q", got)
	}
	if got := normalizeMCPServerKind("generic"); got != mcpServerKindGeneric {
		t.Fatalf("expected generic kind to remain generic, got %q", got)
	}
}

func TestNormalizeMCPServerTransport(t *testing.T) {
	if got := normalizeMCPServerTransport(""); got != "" {
		t.Fatalf("expected empty transport to remain empty before defaulting, got %q", got)
	}
	if got := normalizeMCPServerTransport("streamable-http"); got != mcpTransportStreamableHTTP {
		t.Fatalf("expected streamable-http alias to normalize to %q, got %q", mcpTransportStreamableHTTP, got)
	}
	if got := normalizeMCPServerTransport("stdio"); got != mcpTransportStdio {
		t.Fatalf("expected stdio transport to remain %q, got %q", mcpTransportStdio, got)
	}
}

func TestNormalizeMCPServerConfigKeepsExplicitAuthType(t *testing.T) {
	cfg := normalizeMCPServerConfig(MCPServerConfig{
		Endpoint: "https://mcp.example.com",
		AuthType: "apikey",
		Token:    "",
	})
	if cfg.AuthType != "apikey" {
		t.Fatalf("expected auth_type to remain apikey without token, got %q", cfg.AuthType)
	}
	if cfg.Transport != mcpTransportStreamableHTTP {
		t.Fatalf("expected default transport %q for endpoint config, got %q", mcpTransportStreamableHTTP, cfg.Transport)
	}
}

func TestNormalizeMCPServerConfigStdioDefaults(t *testing.T) {
	cfg := normalizeMCPServerConfig(MCPServerConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "tool"},
		AuthType:  "bearer",
		Token:     "should_not_be_required",
	})
	if cfg.Transport != mcpTransportStdio {
		t.Fatalf("expected stdio transport, got %q", cfg.Transport)
	}
	if cfg.Command != "npx" {
		t.Fatalf("expected command npx, got %q", cfg.Command)
	}
	if cfg.AuthType != "none" {
		t.Fatalf("expected stdio auth_type=none, got %q", cfg.AuthType)
	}
	if cfg.Endpoint != "" {
		t.Fatalf("expected stdio endpoint to be cleared, got %q", cfg.Endpoint)
	}
}

func TestActiveMCPServersIncludesAllKinds(t *testing.T) {
	oc := testAIClientWithMCPServers(map[string]MCPServerConfig{
		"nexus": {
			Endpoint:  "https://nexum.clay.earth/mcp",
			AuthType:  "none",
			Connected: true,
			Kind:      mcpServerKindNexus,
		},
		"docs": {
			Endpoint:  "https://docs.example.com/mcp",
			AuthType:  "none",
			Connected: true,
			Kind:      mcpServerKindGeneric,
		},
	})

	active := oc.activeMCPServers()
	if len(active) != 2 {
		t.Fatalf("expected 2 active MCP servers, got %d", len(active))
	}
}
