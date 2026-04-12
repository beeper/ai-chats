package opencode

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/sdk"
)

type openCodeAgentCatalog struct {
	client *OpenCodeClient
}

func (c openCodeAgentCatalog) DefaultAgent(ctx context.Context, login *bridgev2.UserLogin) (*sdk.Agent, error) {
	agents, err := c.ListAgents(ctx, login)
	if err != nil || len(agents) == 0 {
		return nil, err
	}
	return agents[0], nil
}

func (c openCodeAgentCatalog) ListAgents(_ context.Context, login *bridgev2.UserLogin) ([]*sdk.Agent, error) {
	meta := loginMetadata(login)
	if meta == nil || len(meta.OpenCodeInstances) == 0 {
		return nil, nil
	}
	instanceIDs := sortedOpenCodeInstanceIDs(meta.OpenCodeInstances)
	out := make([]*sdk.Agent, 0, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		displayName := c.client.instanceDisplayName(instanceID)
		out = append(out, openCodeSDKAgent(instanceID, displayName))
	}
	return out, nil
}

func (c openCodeAgentCatalog) ResolveAgent(ctx context.Context, login *bridgev2.UserLogin, identifier string) (*sdk.Agent, error) {
	instanceID, ok := ParseOpenCodeIdentifier(identifier)
	if !ok {
		instanceID = strings.TrimSpace(identifier)
	}
	if instanceID == "" {
		return nil, nil
	}
	meta := loginMetadata(login)
	if meta == nil || meta.OpenCodeInstances == nil {
		return nil, nil
	}
	if _, ok := meta.OpenCodeInstances[instanceID]; !ok {
		return nil, nil
	}
	return openCodeSDKAgent(instanceID, c.client.instanceDisplayName(instanceID)), nil
}

func (oc *OpenCodeClient) sdkAgentCatalog() sdk.AgentCatalog {
	return openCodeAgentCatalog{client: oc}
}

func sortedOpenCodeInstanceIDs(instances map[string]*OpenCodeInstance) []string {
	if len(instances) == 0 {
		return nil
	}
	out := make([]string, 0, len(instances))
	for instanceID := range instances {
		if strings.TrimSpace(instanceID) != "" {
			out = append(out, instanceID)
		}
	}
	slices.Sort(out)
	return out
}

func (oc *OpenCodeClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil {
		return nil, errors.New("login unavailable")
	}
	agent, err := oc.sdkAgentCatalog().ResolveAgent(ctx, oc.UserLogin, identifier)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, fmt.Errorf("unknown identifier: %s", identifier)
	}
	instanceID, _ := ParseOpenCodeIdentifier(identifier)
	if instanceID == "" {
		instanceID, _ = strings.CutPrefix(strings.TrimSpace(agent.ModelKey), "opencode:")
	}

	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, OpenCodeUserID(instanceID))
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenCode ghost: %w", err)
	}
	if oc.bridge != nil {
		oc.bridge.EnsureGhostDisplayName(ctx, instanceID)
	}

	var chat *bridgev2.CreateChatResponse
	if createChat {
		if oc.bridge == nil {
			return nil, errors.New("OpenCode bridge unavailable")
		}
		chat, err = oc.bridge.CreateSessionChat(ctx, instanceID, "", true)
		if err != nil {
			return nil, fmt.Errorf("failed to create OpenCode chat: %w", err)
		}
	}

	return &bridgev2.ResolveIdentifierResponse{
		UserID: OpenCodeUserID(instanceID),
		UserInfo: &bridgev2.UserInfo{
			Name:        ptr.Ptr(agent.Name),
			IsBot:       ptr.Ptr(true),
			Identifiers: slices.Clone(agent.Identifiers),
		},
		Ghost: ghost,
		Chat:  chat,
	}, nil
}

func (oc *OpenCodeClient) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	meta := loginMetadata(oc.UserLogin)
	if meta == nil || len(meta.OpenCodeInstances) == 0 {
		return nil, nil
	}
	instanceIDs := sortedOpenCodeInstanceIDs(meta.OpenCodeInstances)
	out := make([]*bridgev2.ResolveIdentifierResponse, 0, len(instanceIDs))
	for _, instanceID := range instanceIDs {
		resp, err := oc.ResolveIdentifier(ctx, "opencode:"+instanceID, false)
		if err == nil && resp != nil {
			out = append(out, resp)
		}
	}
	return out, nil
}

func (oc *OpenCodeClient) SearchUsers(ctx context.Context, query string) ([]*bridgev2.ResolveIdentifierResponse, error) {
	query = strings.TrimSpace(query)
	contacts, err := oc.GetContactList(ctx)
	if err != nil || query == "" {
		return contacts, err
	}
	out := make([]*bridgev2.ResolveIdentifierResponse, 0, len(contacts))
	for _, contact := range contacts {
		if contact == nil || contact.UserInfo == nil {
			continue
		}
		name := ""
		if contact.UserInfo.Name != nil {
			name = strings.ToLower(strings.TrimSpace(*contact.UserInfo.Name))
		}
		id := strings.ToLower(strings.TrimSpace(string(contact.UserID)))
		identifiers := strings.ToLower(strings.Join(contact.UserInfo.Identifiers, " "))
		q := strings.ToLower(query)
		if strings.Contains(name, q) || strings.Contains(id, q) || strings.Contains(identifiers, q) {
			out = append(out, contact)
		}
	}
	if resp, err := oc.ResolveIdentifier(ctx, query, false); err == nil && resp != nil {
		alreadyIncluded := slices.ContainsFunc(out, func(existing *bridgev2.ResolveIdentifierResponse) bool {
			return existing != nil && existing.UserID == resp.UserID
		})
		if !alreadyIncluded {
			out = append(out, resp)
		}
	}
	return out, nil
}
