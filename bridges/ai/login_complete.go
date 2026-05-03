package ai

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/status"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
	"github.com/beeper/agentremote/sdk"
)

func (ol *OpenAILogin) completeLogin(ctx context.Context, input loginCompletionInput) (*bridgev2.LoginStep, error) {
	provider := normalizeProvider(input.Provider)
	apiKey := strings.TrimSpace(input.APIKey)
	baseURL := stringutil.NormalizeBaseURL(input.BaseURL)
	serviceTokens := input.ServiceTokens
	if ol.User == nil {
		return nil, errAIMissingUserContext
	}

	override := ol.Override
	if override != nil {
		overrideMeta := loginMetadata(override)
		if overrideMeta == nil {
			return nil, errAIMissingReloginMeta
		}
		if !strings.EqualFold(normalizeProvider(overrideMeta.Provider), provider) {
			return nil, sdk.NewLoginRespError(http.StatusBadRequest, fmt.Sprintf("Can't relogin %s account with %s credentials.", overrideMeta.Provider, provider), "AI", "PROVIDER_MISMATCH")
		}
	}

	loginID, ordinal, err := ol.resolveLoginTarget(ctx, provider)
	if err != nil {
		return nil, err
	}

	remoteNameBase := formatRemoteName(provider, apiKey)
	remoteName := remoteNameBase
	if override != nil && strings.TrimSpace(override.RemoteName) != "" {
		remoteName = override.RemoteName
	} else if ordinal > 1 {
		remoteName = fmt.Sprintf("%s (%d)", remoteNameBase, ordinal)
	}

	meta := &UserLoginMetadata{}
	cfg := &aiLoginConfig{}
	if override != nil {
		meta, err = cloneUserLoginMetadata(loginMetadata(override))
		if err != nil {
			return nil, sdk.WrapLoginRespError(fmt.Errorf("failed to clone relogin metadata: %w", err), http.StatusInternalServerError, "AI", "CLONE_RELOGIN_METADATA_FAILED")
		}
		cfg, err = loadAILoginConfig(ctx, override)
		if err != nil {
			return nil, sdk.WrapLoginRespError(fmt.Errorf("failed to load relogin config: %w", err), http.StatusInternalServerError, "AI", "LOAD_RELOGIN_CONFIG_FAILED")
		}
	}
	if meta == nil {
		meta = &UserLoginMetadata{}
	}
	if cfg == nil {
		cfg = &aiLoginConfig{}
	}
	meta.Provider = provider
	creds := &LoginCredentials{
		APIKey:  apiKey,
		BaseURL: baseURL,
	}
	if serviceTokens != nil && !serviceTokensEmpty(serviceTokens) {
		creds.ServiceTokens = cloneServiceTokens(serviceTokens)
	}
	if loginCredentialsEmpty(creds) {
		cfg.Credentials = nil
	} else {
		cfg.Credentials = creds
	}
	if err := ol.validateLoginMetadata(ctx, loginID, meta.Provider, cfg); err != nil {
		return nil, err
	}

	login, step, err := sdk.PersistAndCompleteLoginWithOptions(
		ctx,
		context.Background(),
		ol.User,
		&database.UserLogin{
			ID:         loginID,
			RemoteName: remoteName,
			Metadata:   meta,
		},
		"com.beeper.agentremote.ai.complete",
		sdk.PersistLoginCompletionOptions{
			NewLoginParams: &bridgev2.NewLoginParams{
				DeleteOnConflict: ol.Override != nil,
				LoadUserLogin: func(loadCtx context.Context, login *bridgev2.UserLogin) error {
					if ol.Connector == nil {
						return nil
					}
					return ol.Connector.loadAIUserLogin(loadCtx, login, meta, cfg)
				},
			},
			AfterPersist: func(saveCtx context.Context, login *bridgev2.UserLogin) error {
				return saveAILoginConfig(saveCtx, login, cfg)
			},
			Cleanup: func(cleanupCtx context.Context, login *bridgev2.UserLogin) {
				if login == nil {
					return
				}
				login.Delete(cleanupCtx, status.BridgeState{}, bridgev2.DeleteOpts{
					DontCleanupRooms: true,
					BlockingCleanup:  true,
				})
			},
		},
	)
	if err != nil {
		code := "CREATE_LOGIN_FAILED"
		if login != nil {
			code = "SAVE_LOGIN_FAILED"
		}
		return nil, sdk.WrapLoginRespError(fmt.Errorf("failed to complete login: %w", err), http.StatusInternalServerError, "AI", code)
	}
	return step, nil
}
