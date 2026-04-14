package ai

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/shared/jsonutil"
)

type aiLoginConfig struct {
	Credentials          *LoginCredentials `json:"credentials,omitempty"`
	TitleGenerationModel string            `json:"title_generation_model,omitempty"`
	Agents               *bool             `json:"agents,omitempty"`
	Timezone             string            `json:"timezone,omitempty"`
	Profile              *UserProfile      `json:"profile,omitempty"`
	Gravatar             *GravatarState    `json:"gravatar,omitempty"`
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
		Gravatar:             cloneGravatarState(src.Gravatar),
	}
}

func loadAILoginConfig(ctx context.Context, login *bridgev2.UserLogin) (*aiLoginConfig, error) {
	_ = ctx
	if login == nil {
		return &aiLoginConfig{}, nil
	}
	meta := loginMetadata(login)
	if meta == nil {
		return &aiLoginConfig{}, nil
	}
	return &aiLoginConfig{
		Credentials:          cloneLoginCredentials(meta.Credentials),
		TitleGenerationModel: meta.TitleGenerationModel,
		Agents:               cloneBoolPtr(meta.Agents),
		Timezone:             meta.Timezone,
		Profile:              cloneUserProfile(meta.Profile),
		Gravatar:             cloneGravatarState(meta.Gravatar),
	}, nil
}

func saveAILoginConfig(ctx context.Context, login *bridgev2.UserLogin, cfg *aiLoginConfig) error {
	if login == nil || cfg == nil {
		return nil
	}
	meta := loginMetadata(login)
	if meta != nil {
		meta.Credentials = cloneLoginCredentials(cfg.Credentials)
		meta.TitleGenerationModel = cfg.TitleGenerationModel
		meta.Agents = cloneBoolPtr(cfg.Agents)
		meta.Timezone = cfg.Timezone
		meta.Profile = cloneUserProfile(cfg.Profile)
		meta.Gravatar = cloneGravatarState(cfg.Gravatar)
		if err := login.Save(ctx); err != nil {
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
