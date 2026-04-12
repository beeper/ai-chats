package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

type aiLoginConfig struct {
	Credentials          *LoginCredentials         `json:"credentials,omitempty"`
	TitleGenerationModel string                    `json:"title_generation_model,omitempty"`
	Agents               *bool                     `json:"agents,omitempty"`
	ModelCache           *ModelCache               `json:"model_cache,omitempty"`
	Gravatar             *GravatarState            `json:"gravatar,omitempty"`
	Timezone             string                    `json:"timezone,omitempty"`
	Profile              *UserProfile              `json:"profile,omitempty"`
	FileAnnotationCache  map[string]FileAnnotation `json:"file_annotation_cache,omitempty"`
	ConsecutiveErrors    int                       `json:"consecutive_errors,omitempty"`
	LastErrorAt          int64                     `json:"last_error_at,omitempty"`
}

func aiLoginConfigFromMetadata(meta *UserLoginMetadata) *aiLoginConfig {
	if meta == nil {
		return &aiLoginConfig{}
	}
	return &aiLoginConfig{
		Credentials:          cloneLoginCredentials(meta.Credentials),
		TitleGenerationModel: meta.TitleGenerationModel,
		Agents:               cloneBoolPtr(meta.Agents),
		ModelCache:           cloneModelCache(meta.ModelCache),
		Gravatar:             cloneGravatarState(meta.Gravatar),
		Timezone:             meta.Timezone,
		Profile:              cloneUserProfile(meta.Profile),
		FileAnnotationCache:  cloneFileAnnotationCache(meta.FileAnnotationCache),
		ConsecutiveErrors:    meta.ConsecutiveErrors,
		LastErrorAt:          meta.LastErrorAt,
	}
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
			primary.Profile = maps.Clone(src.Primary.Profile)
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
		ModelCache:           cloneModelCache(src.ModelCache),
		Gravatar:             cloneGravatarState(src.Gravatar),
		Timezone:             src.Timezone,
		Profile:              cloneUserProfile(src.Profile),
		FileAnnotationCache:  cloneFileAnnotationCache(src.FileAnnotationCache),
		ConsecutiveErrors:    src.ConsecutiveErrors,
		LastErrorAt:          src.LastErrorAt,
	}
}

func loginMetadataView(provider string, cfg *aiLoginConfig) *UserLoginMetadata {
	meta := &UserLoginMetadata{Provider: provider}
	if cfg == nil {
		return meta
	}
	meta.Credentials = cloneLoginCredentials(cfg.Credentials)
	meta.TitleGenerationModel = cfg.TitleGenerationModel
	meta.Agents = cloneBoolPtr(cfg.Agents)
	meta.ModelCache = cloneModelCache(cfg.ModelCache)
	meta.Gravatar = cloneGravatarState(cfg.Gravatar)
	meta.Timezone = cfg.Timezone
	meta.Profile = cloneUserProfile(cfg.Profile)
	meta.FileAnnotationCache = cloneFileAnnotationCache(cfg.FileAnnotationCache)
	meta.ConsecutiveErrors = cfg.ConsecutiveErrors
	meta.LastErrorAt = cfg.LastErrorAt
	return meta
}

func ensureAILoginConfigTable(ctx context.Context, login *bridgev2.UserLogin) error {
	db := bridgeDBFromLogin(login)
	if db == nil || login == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return nil
	}
	_, err := db.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS `+aiLoginConfigTable+` (
			bridge_id TEXT NOT NULL,
			login_id TEXT NOT NULL,
			config_json TEXT NOT NULL DEFAULT '',
			updated_at_ms INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (bridge_id, login_id)
		)
	`)
	return err
}

func loadAILoginConfig(ctx context.Context, login *bridgev2.UserLogin) (*aiLoginConfig, error) {
	db := bridgeDBFromLogin(login)
	if db == nil || login == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return &aiLoginConfig{}, nil
	}
	if err := ensureAILoginConfigTable(ctx, login); err != nil {
		return nil, err
	}
	var raw string
	err := db.QueryRow(ctx, `
		SELECT config_json
		FROM `+aiLoginConfigTable+`
		WHERE bridge_id=$1 AND login_id=$2
	`, string(login.Bridge.DB.BridgeID), string(login.ID)).Scan(&raw)
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
	db := bridgeDBFromLogin(login)
	if db != nil && login.Bridge != nil && login.Bridge.DB != nil {
		if err := ensureAILoginConfigTable(ctx, login); err != nil {
			return err
		}
		payload, err := json.Marshal(cfg)
		if err != nil {
			return err
		}
		if _, err = db.Exec(ctx, `
			INSERT INTO `+aiLoginConfigTable+` (bridge_id, login_id, config_json, updated_at_ms)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (bridge_id, login_id) DO UPDATE SET
				config_json=excluded.config_json,
				updated_at_ms=excluded.updated_at_ms
		`, string(login.Bridge.DB.BridgeID), string(login.ID), string(payload), time.Now().UnixMilli()); err != nil {
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

func saveAIUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	if login == nil {
		return nil
	}
	meta := loginMetadata(login)
	if err := saveAILoginConfig(ctx, login, aiLoginConfigFromMetadata(meta)); err != nil {
		return err
	}
	if meta == nil || meta.CustomAgents == nil {
		return nil
	}
	current, err := listCustomAgentsForLogin(ctx, login)
	if err != nil {
		return err
	}
	for agentID := range current {
		if _, ok := meta.CustomAgents[agentID]; !ok {
			if err = deleteCustomAgentForLogin(ctx, login, agentID); err != nil {
				return err
			}
		}
	}
	for _, agent := range meta.CustomAgents {
		if err = saveCustomAgentForLogin(ctx, login, agent); err != nil {
			return err
		}
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
