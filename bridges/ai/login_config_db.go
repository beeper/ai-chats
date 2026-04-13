package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

type aiLoginConfig struct {
	Credentials          *LoginCredentials `json:"credentials,omitempty"`
	TitleGenerationModel string            `json:"title_generation_model,omitempty"`
	Agents               *bool             `json:"agents,omitempty"`
	Timezone             string            `json:"timezone,omitempty"`
	Profile              *UserProfile      `json:"profile,omitempty"`
}

func cloneBoolPtr(src *bool) *bool {
	if src == nil {
		return nil
	}
	v := *src
	return &v
}

func cloneLoginCredentials(src *LoginCredentials) *LoginCredentials {
	if src == nil {
		return nil
	}
	clone := *src
	clone.ServiceTokens = cloneServiceTokens(src.ServiceTokens)
	return &clone
}

func cloneModelCache(src *ModelCache) *ModelCache {
	if src == nil {
		return nil
	}
	clone := *src
	clone.Models = slices.Clone(src.Models)
	return &clone
}

func cloneGravatarState(src *GravatarState) *GravatarState {
	if src == nil {
		return nil
	}
	clone := *src
	if src.Primary != nil {
		primary := *src.Primary
		if src.Primary.Profile != nil {
			primary.Profile = jsonutil.DeepCloneMap(src.Primary.Profile)
		}
		clone.Primary = &primary
	}
	return &clone
}

func cloneUserProfile(src *UserProfile) *UserProfile {
	if src == nil {
		return nil
	}
	clone := *src
	return &clone
}

func cloneFileAnnotationCache(src map[string]FileAnnotation) map[string]FileAnnotation {
	if len(src) == 0 {
		return nil
	}
	return maps.Clone(src)
}

func cloneAILoginConfig(src *aiLoginConfig) *aiLoginConfig {
	if src == nil {
		return &aiLoginConfig{}
	}
	return &aiLoginConfig{
		Credentials:          cloneLoginCredentials(src.Credentials),
		TitleGenerationModel: src.TitleGenerationModel,
		Agents:               cloneBoolPtr(src.Agents),
		Timezone:             src.Timezone,
		Profile:              cloneUserProfile(src.Profile),
	}
}

func loadAILoginConfig(ctx context.Context, login *bridgev2.UserLogin) (*aiLoginConfig, error) {
	scope := loginScopeForLogin(login)
	if scope == nil {
		return &aiLoginConfig{}, nil
	}
	var raw string
	err := scope.db.QueryRow(ctx, `
		SELECT config_json
		FROM `+aiLoginConfigTable+`
	WHERE bridge_id=$1 AND login_id=$2
	`, scope.bridgeID, scope.loginID).Scan(&raw)
	if err == sql.ErrNoRows || raw == "" {
		return &aiLoginConfig{}, nil
	}
	if err != nil {
		return nil, err
	}
	var persisted aiLoginConfig
	if err = json.Unmarshal([]byte(raw), &persisted); err != nil {
		return nil, err
	}
	return &persisted, nil
}

func saveAILoginConfig(ctx context.Context, login *bridgev2.UserLogin, cfg *aiLoginConfig) error {
	if login == nil || cfg == nil {
		return nil
	}
	if scope := loginScopeForLogin(login); scope != nil {
		payload, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if _, err = scope.db.Exec(ctx, `
			INSERT INTO `+aiLoginConfigTable+` (bridge_id, login_id, config_json, updated_at_ms)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (bridge_id, login_id) DO UPDATE SET
				config_json=excluded.config_json,
				updated_at_ms=excluded.updated_at_ms
		`, scope.bridgeID, scope.loginID, string(payload), time.Now().UnixMilli()); err != nil {
			return err
		}
	}
	if client, ok := login.Client.(*AIClient); ok && client != nil {
		client.loginConfigMu.Lock()
		client.loginConfig = cloneAILoginConfig(cfg)
		client.loginConfigMu.Unlock()
	}
	return nil
}

func (oc *AIClient) ensureLoginConfigLoaded(ctx context.Context) *aiLoginConfig {
	if oc == nil {
		return &aiLoginConfig{}
	}
	oc.loginConfigMu.Lock()
	defer oc.loginConfigMu.Unlock()
	if oc.loginConfig != nil {
		return oc.loginConfig
	}
	cfg, err := loadAILoginConfig(ctx, oc.UserLogin)
	if err != nil {
		oc.loggerForContext(ctx).Warn().Err(err).Msg("Failed to load AI login config")
		cfg = &aiLoginConfig{}
	}
	oc.loginConfig = cfg
	return oc.loginConfig
}

func (oc *AIClient) loginConfigSnapshot(ctx context.Context) *aiLoginConfig {
	return cloneAILoginConfig(oc.ensureLoginConfigLoaded(ctx))
}

func (oc *AIClient) updateLoginConfig(ctx context.Context, fn func(*aiLoginConfig) bool) error {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	oc.loginConfigMu.Lock()
	defer oc.loginConfigMu.Unlock()
	if oc.loginConfig == nil {
		cfg, err := loadAILoginConfig(ctx, oc.UserLogin)
		if err != nil {
			return err
		}
		oc.loginConfig = cfg
	}
	if !fn(oc.loginConfig) {
		return nil
	}
	return saveAILoginConfig(ctx, oc.UserLogin, oc.loginConfig)
}

func (oc *AIClient) replaceLoginConfig(ctx context.Context, cfg *aiLoginConfig) error {
	if oc == nil || oc.UserLogin == nil {
		return nil
	}
	oc.loginConfigMu.Lock()
	oc.loginConfig = cloneAILoginConfig(cfg)
	oc.loginConfigMu.Unlock()
	return saveAILoginConfig(ctx, oc.UserLogin, cfg)
}
