package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"maunium.net/go/mautrix/bridgev2"
)

type aiPersistedLoginConfig struct {
	Credentials          *LoginCredentials                  `json:"credentials,omitempty"`
	TitleGenerationModel string                             `json:"title_generation_model,omitempty"`
	Agents               *bool                              `json:"agents,omitempty"`
	ModelCache           *ModelCache                        `json:"model_cache,omitempty"`
	Gravatar             *GravatarState                     `json:"gravatar,omitempty"`
	Timezone             string                             `json:"timezone,omitempty"`
	Profile              *UserProfile                       `json:"profile,omitempty"`
	FileAnnotationCache  map[string]FileAnnotation          `json:"file_annotation_cache,omitempty"`
	CustomAgents         map[string]*AgentDefinitionContent `json:"custom_agents,omitempty"`
	ConsecutiveErrors    int                                `json:"consecutive_errors,omitempty"`
	LastErrorAt          int64                              `json:"last_error_at,omitempty"`
}

func compactAIUserLoginMetadata(meta *UserLoginMetadata) *UserLoginMetadata {
	if meta == nil {
		return &UserLoginMetadata{}
	}
	return &UserLoginMetadata{Provider: meta.Provider}
}

func aiPersistedLoginConfigFromMeta(meta *UserLoginMetadata) *aiPersistedLoginConfig {
	if meta == nil {
		return &aiPersistedLoginConfig{}
	}
	return &aiPersistedLoginConfig{
		Credentials:          meta.Credentials,
		TitleGenerationModel: meta.TitleGenerationModel,
		Agents:               meta.Agents,
		ModelCache:           meta.ModelCache,
		Gravatar:             meta.Gravatar,
		Timezone:             meta.Timezone,
		Profile:              meta.Profile,
		FileAnnotationCache:  meta.FileAnnotationCache,
		CustomAgents:         meta.CustomAgents,
		ConsecutiveErrors:    meta.ConsecutiveErrors,
		LastErrorAt:          meta.LastErrorAt,
	}
}

func applyAIPersistedLoginConfig(meta *UserLoginMetadata, persisted *aiPersistedLoginConfig) {
	if meta == nil || persisted == nil {
		return
	}
	meta.Credentials = persisted.Credentials
	meta.TitleGenerationModel = persisted.TitleGenerationModel
	meta.Agents = persisted.Agents
	meta.ModelCache = persisted.ModelCache
	meta.Gravatar = persisted.Gravatar
	meta.Timezone = persisted.Timezone
	meta.Profile = persisted.Profile
	meta.FileAnnotationCache = persisted.FileAnnotationCache
	meta.CustomAgents = persisted.CustomAgents
	meta.ConsecutiveErrors = persisted.ConsecutiveErrors
	meta.LastErrorAt = persisted.LastErrorAt
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

func loadAIUserLoginConfig(ctx context.Context, login *bridgev2.UserLogin, meta *UserLoginMetadata) error {
	db := bridgeDBFromLogin(login)
	if db == nil || login == nil || login.Bridge == nil || login.Bridge.DB == nil || meta == nil {
		return nil
	}
	if err := ensureAILoginConfigTable(ctx, login); err != nil {
		return err
	}
	var raw string
	err := db.QueryRow(ctx, `
		SELECT config_json
		FROM `+aiLoginConfigTable+`
		WHERE bridge_id=$1 AND login_id=$2
	`, string(login.Bridge.DB.BridgeID), string(login.ID)).Scan(&raw)
	if err == sql.ErrNoRows || raw == "" {
		return nil
	}
	if err != nil {
		return err
	}
	var persisted aiPersistedLoginConfig
	if err = json.Unmarshal([]byte(raw), &persisted); err != nil {
		return err
	}
	applyAIPersistedLoginConfig(meta, &persisted)
	login.Metadata = meta
	return nil
}

func saveAIUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	if login == nil {
		return nil
	}
	meta := loginMetadata(login)
	db := bridgeDBFromLogin(login)
	if db != nil && login.Bridge != nil && login.Bridge.DB != nil {
		if err := ensureAILoginConfigTable(ctx, login); err != nil {
			return err
		}
		payload, err := json.Marshal(aiPersistedLoginConfigFromMeta(meta))
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
	original := login.Metadata
	login.Metadata = compactAIUserLoginMetadata(meta)
	err := login.Save(ctx)
	login.Metadata = original
	return err
}
