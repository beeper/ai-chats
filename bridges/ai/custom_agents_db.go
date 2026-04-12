package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
)

func cloneAgentDefinitionContentMap(src map[string]*AgentDefinitionContent) map[string]*AgentDefinitionContent {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]*AgentDefinitionContent, len(src))
	for id, agent := range src {
		if agent == nil {
			continue
		}
		data, err := json.Marshal(agent)
		if err != nil {
			clone := *agent
			out[id] = &clone
			continue
		}
		var clone AgentDefinitionContent
		if err = json.Unmarshal(data, &clone); err != nil {
			fallback := *agent
			out[id] = &fallback
			continue
		}
		out[id] = &clone
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type customAgentScope struct {
	db       *dbutil.Database
	bridgeID string
	loginID  string
}

func customAgentScopeForLogin(login *bridgev2.UserLogin) *customAgentScope {
	db := bridgeDBFromLogin(login)
	if login == nil || db == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return nil
	}
	bridgeID := strings.TrimSpace(string(login.Bridge.DB.BridgeID))
	loginID := strings.TrimSpace(string(login.ID))
	if bridgeID == "" || loginID == "" {
		return nil
	}
	return &customAgentScope{db: db, bridgeID: bridgeID, loginID: loginID}
}

func customAgentScopeForClient(client *AIClient) *customAgentScope {
	if client == nil {
		return nil
	}
	return customAgentScopeForLogin(client.UserLogin)
}

func listCustomAgentsForLogin(ctx context.Context, login *bridgev2.UserLogin) (map[string]*AgentDefinitionContent, error) {
	scope := customAgentScopeForLogin(login)
	if scope == nil {
		return nil, nil
	}
	rows, err := scope.db.Query(ctx, `
		SELECT agent_id, content_json
		FROM `+aiCustomAgentsTable+`
		WHERE bridge_id=$1 AND login_id=$2
	`, scope.bridgeID, scope.loginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	agents := make(map[string]*AgentDefinitionContent)
	for rows.Next() {
		var agentID string
		var raw string
		if err = rows.Scan(&agentID, &raw); err != nil {
			return nil, err
		}
		agentID = strings.TrimSpace(agentID)
		if agentID == "" || strings.TrimSpace(raw) == "" {
			continue
		}
		var content AgentDefinitionContent
		if err = json.Unmarshal([]byte(raw), &content); err != nil {
			return nil, err
		}
		agent := content
		agents[agentID] = &agent
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(agents) == 0 {
		return nil, nil
	}
	return agents, nil
}

func saveCustomAgentForLogin(ctx context.Context, login *bridgev2.UserLogin, agent *AgentDefinitionContent) error {
	scope := customAgentScopeForLogin(login)
	if scope == nil || agent == nil {
		return nil
	}
	payload, err := json.Marshal(agent)
	if err != nil {
		return err
	}
	_, err = scope.db.Exec(ctx, `
		INSERT INTO `+aiCustomAgentsTable+` (
			bridge_id, login_id, agent_id, content_json, updated_at_ms
		) VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (bridge_id, login_id, agent_id) DO UPDATE SET
			content_json=excluded.content_json,
			updated_at_ms=excluded.updated_at_ms
	`, scope.bridgeID, scope.loginID, strings.TrimSpace(agent.ID), string(payload), time.Now().UnixMilli())
	return err
}

func deleteCustomAgentForLogin(ctx context.Context, login *bridgev2.UserLogin, agentID string) error {
	scope := customAgentScopeForLogin(login)
	if scope == nil || strings.TrimSpace(agentID) == "" {
		return nil
	}
	_, err := scope.db.Exec(ctx, `
		DELETE FROM `+aiCustomAgentsTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3
	`, scope.bridgeID, scope.loginID, strings.TrimSpace(agentID))
	return err
}

func loadCustomAgentForLogin(ctx context.Context, login *bridgev2.UserLogin, agentID string) (*AgentDefinitionContent, error) {
	scope := customAgentScopeForLogin(login)
	if scope == nil || strings.TrimSpace(agentID) == "" {
		return nil, nil
	}
	var raw string
	err := scope.db.QueryRow(ctx, `
		SELECT content_json
		FROM `+aiCustomAgentsTable+`
		WHERE bridge_id=$1 AND login_id=$2 AND agent_id=$3
	`, scope.bridgeID, scope.loginID, strings.TrimSpace(agentID)).Scan(&raw)
	if err == sql.ErrNoRows || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var content AgentDefinitionContent
	if err = json.Unmarshal([]byte(raw), &content); err != nil {
		return nil, err
	}
	return &content, nil
}
