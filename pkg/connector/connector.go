package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/configupgrade"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	defaultTemperature          = 0.0 // Unset by default; provider/model default is used.
	defaultMaxContextMessages   = 20
	defaultGroupContextMessages = 20
	defaultMaxTokens            = 512
	defaultReasoningEffort      = "low"
)

var (
	_ bridgev2.NetworkConnector               = (*OpenAIConnector)(nil)
	_ bridgev2.PortalBridgeInfoFillingNetwork = (*OpenAIConnector)(nil)
)

// OpenAIConnector wires mautrix bridgev2 to the OpenAI chat APIs.
type OpenAIConnector struct {
	br     *bridgev2.Bridge
	Config Config

	clientsMu sync.Mutex
	clients   map[networkid.UserLoginID]bridgev2.NetworkAPI
}

func (oc *OpenAIConnector) Init(bridge *bridgev2.Bridge) {
	oc.br = bridge
	oc.clientsMu.Lock()
	if oc.clients == nil {
		oc.clients = make(map[networkid.UserLoginID]bridgev2.NetworkAPI)
	}
	oc.clientsMu.Unlock()
}

func (oc *OpenAIConnector) Stop(ctx context.Context) {
	oc.clientsMu.Lock()
	clients := make(map[networkid.UserLoginID]bridgev2.NetworkAPI, len(oc.clients))
	for k, v := range oc.clients {
		clients[k] = v
	}
	oc.clientsMu.Unlock()

	for _, client := range clients {
		if dc, ok := client.(interface{ Disconnect() }); ok {
			dc.Disconnect()
		}
	}
}

func (oc *OpenAIConnector) Start(ctx context.Context) error {
	oc.applyRuntimeDefaults()

	// Register AI commands with the command processor
	if proc, ok := oc.br.Commands.(*commands.Processor); ok {
		oc.registerCommands(proc)
		oc.br.Log.Info().Msg("Registered AI commands with command processor")
	} else {
		oc.br.Log.Warn().Type("commands_type", oc.br.Commands).Msg("Failed to register AI commands: command processor type assertion failed")
	}

	// Register custom Matrix event handlers
	oc.registerCustomEventHandlers()

	// Initialize provisioning API endpoints
	oc.initProvisioning()

	return nil
}

func (oc *OpenAIConnector) applyRuntimeDefaults() {
	if oc.Config.ModelCacheDuration == 0 {
		oc.Config.ModelCacheDuration = 6 * time.Hour
	}
	if oc.Config.Bridge.CommandPrefix == "" {
		oc.Config.Bridge.CommandPrefix = "!ai"
	}
	if oc.Config.Codex == nil {
		oc.Config.Codex = &CodexConfig{}
	}
	if oc.Config.Codex.Enabled == nil {
		v := true
		oc.Config.Codex.Enabled = &v
	}
	if strings.TrimSpace(oc.Config.Codex.Command) == "" {
		oc.Config.Codex.Command = "codex"
	}
	if strings.TrimSpace(oc.Config.Codex.DefaultModel) == "" {
		oc.Config.Codex.DefaultModel = "gpt-5.1-codex"
	}
	if oc.Config.Codex.NetworkAccess == nil {
		v := true
		oc.Config.Codex.NetworkAccess = &v
	}
	if oc.Config.Codex.ClientInfo == nil {
		oc.Config.Codex.ClientInfo = &CodexClientInfo{}
	}
	if strings.TrimSpace(oc.Config.Codex.ClientInfo.Name) == "" {
		oc.Config.Codex.ClientInfo.Name = "ai_bridge_matrix"
	}
	if strings.TrimSpace(oc.Config.Codex.ClientInfo.Title) == "" {
		oc.Config.Codex.ClientInfo.Title = "AI Bridge (Matrix)"
	}
	if strings.TrimSpace(oc.Config.Codex.ClientInfo.Version) == "" {
		oc.Config.Codex.ClientInfo.Version = "0.1.0"
	}
	if oc.Config.OpenCode != nil {
		if oc.Config.OpenCode.Enabled == nil {
			v := true
			oc.Config.OpenCode.Enabled = &v
		}
		if oc.Config.OpenCode.AutoStart == nil {
			v := false
			oc.Config.OpenCode.AutoStart = &v
		}
		if strings.TrimSpace(oc.Config.OpenCode.Command) == "" {
			oc.Config.OpenCode.Command = "opencode"
		}
		if strings.TrimSpace(oc.Config.OpenCode.Hostname) == "" {
			oc.Config.OpenCode.Hostname = "127.0.0.1"
		}
		if strings.TrimSpace(oc.Config.OpenCode.Username) == "" {
			oc.Config.OpenCode.Username = "opencode"
		}
	}
	if oc.Config.Pruning == nil {
		oc.Config.Pruning = DefaultPruningConfig()
	} else {
		oc.Config.Pruning = applyPruningDefaults(oc.Config.Pruning)
	}
}

// SetMatrixCredentials seeds Beeper provider config from the Matrix account, if unset.
func (oc *OpenAIConnector) SetMatrixCredentials(accessToken, homeserver string) {
	if oc == nil {
		return
	}
	if oc.Config.Beeper.BaseURL == "" && strings.TrimSpace(homeserver) != "" {
		oc.Config.Beeper.BaseURL = strings.TrimSpace(homeserver)
	}
	if oc.Config.Beeper.Token == "" && strings.TrimSpace(accessToken) != "" {
		oc.Config.Beeper.Token = strings.TrimSpace(accessToken)
	}
}

// registerCustomEventHandlers registers handlers for custom Matrix state events
func (oc *OpenAIConnector) registerCustomEventHandlers() {
	// Type assert the Matrix connector to get the concrete type with EventProcessor
	matrixConnector, ok := oc.br.Matrix.(*matrix.Connector)
	if !ok {
		oc.br.Log.Warn().Msg("Cannot register custom event handlers: Matrix connector type assertion failed")
		return
	}

	// Register handler for direct room settings state events
	matrixConnector.EventProcessor.On(RoomSettingsEventType, oc.handleRoomSettingsEvent)

	// Register handler for BeeperSendState wrapper events (desktop E2EE state updates)
	matrixConnector.EventProcessor.On(event.BeeperSendState, oc.handleBeeperSendStateEvent)

	oc.br.Log.Info().
		Str("beeper_send_state_type", event.BeeperSendState.Type).
		Str("beeper_send_state_class", event.BeeperSendState.Class.Name()).
		Msg("Registered room settings event handlers (direct and BeeperSendState)")
}

// handleRoomSettingsEvent processes Matrix room settings state events from users
func (oc *OpenAIConnector) handleRoomSettingsEvent(ctx context.Context, evt *event.Event) {
	log := oc.br.Log.With().
		Str("component", "room_settings_handler").
		Str("room_id", evt.RoomID.String()).
		Str("sender", evt.Sender.String()).
		Logger()

	// Parse event content
	var content RoomSettingsEventContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &content); err != nil {
		log.Warn().Err(err).Msg("Failed to parse room settings event content")
		return
	}

	oc.processRoomSettingsContent(ctx, evt.RoomID, evt.Sender, &content, log)
}

// processRoomSettingsContent handles the common logic for updating portal settings
// Called by both handleRoomSettingsEvent and handleBeeperSendStateEvent
func (oc *OpenAIConnector) processRoomSettingsContent(
	ctx context.Context,
	roomID id.RoomID,
	sender id.UserID,
	content *RoomSettingsEventContent,
	log zerolog.Logger,
) {
	// Look up portal by Matrix room ID
	portal, err := oc.br.GetPortalByMXID(ctx, roomID)
	if err != nil {
		log.Err(err).Msg("Failed to get portal for room settings event")
		return
	}
	if portal == nil {
		log.Debug().Msg("No portal found for room, ignoring settings event")
		return
	}

	// Get the user who sent the event and their login
	user, err := oc.br.GetUserByMXID(ctx, sender)
	if err != nil || user == nil {
		log.Warn().Err(err).Msg("Failed to get user for room settings event")
		return
	}

	// Use getLoginForPortal to find the correct login based on portal's receiver
	// This ensures we use the right provider when user has multiple accounts
	login := oc.getLoginForPortal(ctx, user, portal)
	if login == nil {
		log.Warn().Msg("User has no active login, cannot process settings")
		return
	}

	client, ok := login.Client.(*AIClient)
	if !ok || client == nil {
		log.Warn().Msg("Invalid client type for user login")
		return
	}

	// Validate model if specified
	if content.Model != "" {
		resolved, valid, err := client.resolveModelID(ctx, content.Model)
		if err != nil {
			log.Warn().Err(err).Str("model", content.Model).Msg("Failed to validate model")
		} else if !valid {
			log.Warn().Str("model", content.Model).Msg("Invalid model specified, ignoring")
			client.sendSystemNotice(ctx, portal, fmt.Sprintf("Invalid model: %s. Configuration not applied.", content.Model))
			return
		}
		content.Model = resolved
	}

	// Update portal metadata
	client.updatePortalConfig(ctx, portal, content)

	// Send confirmation notice
	var changes []string
	if content.Model != "" {
		changes = append(changes, fmt.Sprintf("model=%s", content.Model))
	}
	if content.Temperature != nil {
		changes = append(changes, fmt.Sprintf("temperature=%.2f", *content.Temperature))
	}
	if content.MaxContextMessages > 0 {
		changes = append(changes, fmt.Sprintf("context=%d messages", content.MaxContextMessages))
	}
	if content.MaxCompletionTokens > 0 {
		changes = append(changes, fmt.Sprintf("max_tokens=%d", content.MaxCompletionTokens))
	}
	if content.SystemPrompt != "" {
		changes = append(changes, "system_prompt updated")
	}
	if content.ReasoningEffort != "" {
		changes = append(changes, fmt.Sprintf("reasoning_effort=%s", content.ReasoningEffort))
	}
	if content.ConversationMode != "" {
		changes = append(changes, fmt.Sprintf("conversation_mode=%s", content.ConversationMode))
	}
	if len(changes) > 0 {
		client.sendSystemNotice(ctx, portal, fmt.Sprintf("Configuration updated: %s", strings.Join(changes, ", ")))
	}

	logEvent := log.Info().Str("model", content.Model)
	if content.Temperature != nil {
		logEvent = logEvent.Float64("temperature", *content.Temperature)
	}
	logEvent.Msg("Updated room settings from state event")
}

// handleBeeperSendStateEvent processes com.beeper.send_state wrapper events
// This is used by the desktop client to send state events in encrypted rooms
func (oc *OpenAIConnector) handleBeeperSendStateEvent(ctx context.Context, evt *event.Event) {
	log := oc.br.Log.With().
		Str("component", "beeper_send_state_handler").
		Str("room_id", evt.RoomID.String()).
		Str("sender", evt.Sender.String()).
		Str("event_type", evt.Type.Type).
		Str("event_class", evt.Type.Class.Name()).
		Logger()

	log.Info().RawJSON("raw_content", evt.Content.VeryRaw).Msg("Received BeeperSendState event")

	// Parse the wrapper content
	var wrapperContent event.BeeperSendStateEventContent
	if err := json.Unmarshal(evt.Content.VeryRaw, &wrapperContent); err != nil {
		log.Debug().Err(err).Msg("Failed to parse BeeperSendState content")
		return
	}

	// Only process AI room settings events
	if wrapperContent.Type != RoomSettingsEventType.Type {
		return
	}

	log.Debug().
		Str("inner_type", wrapperContent.Type).
		Str("state_key", wrapperContent.StateKey).
		Msg("Processing BeeperSendState wrapper for AI room settings")

	// Parse the inner room settings content
	var content RoomSettingsEventContent
	if err := json.Unmarshal(wrapperContent.Content.VeryRaw, &content); err != nil {
		log.Warn().Err(err).Msg("Failed to parse inner room settings content")
		return
	}

	// Reuse existing handler logic with the parsed content
	oc.processRoomSettingsContent(ctx, evt.RoomID, evt.Sender, &content, log)
}

func (oc *OpenAIConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return &bridgev2.NetworkGeneralCapabilities{
		// Enable disappearing messages - we just delete from Matrix and DB
		DisappearingMessages: true,
		Provisioning: bridgev2.ProvisioningCapabilities{
			ResolveIdentifier: bridgev2.ResolveIdentifierCapabilities{
				CreateDM:       true,
				LookupUsername: true,
				ContactList:    true,
				Search:         true,
			},
		},
	}
}

func (oc *OpenAIConnector) GetBridgeInfoVersion() (info, capabilities int) {
	// Bump capabilities version when room features change.
	// v2: Added UpdateBridgeInfo call on model switch to properly broadcast capability changes
	return 1, 3
}

// FillPortalBridgeInfo sets custom room type for AI rooms
func (oc *OpenAIConnector) FillPortalBridgeInfo(portal *bridgev2.Portal, content *event.BridgeEventContent) {
	meta := portalMeta(portal)
	if meta.IsCronRoom {
		// Cron rooms are hidden by clients
		content.BeeperRoomTypeV2 = "cron"
	} else {
		content.BeeperRoomTypeV2 = "ai"
	}
}

func (oc *OpenAIConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "Beeper AI",
		NetworkURL:           "https://www.beeper.com/ai",
		NetworkIcon:          "mxc://beeper.com/51a668657dd9e0132cc823ad9402c6c2d0fc3321",
		NetworkID:            "ai",
		BeeperBridgeType:     "ai",
		DefaultPort:          29345,
		DefaultCommandPrefix: oc.Config.Bridge.CommandPrefix,
	}
}

func (oc *OpenAIConnector) GetConfig() (example string, data any, upgrader configupgrade.Upgrader) {
	return exampleNetworkConfig, &oc.Config, configupgrade.SimpleUpgrader(upgradeConfig)
}

func (oc *OpenAIConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		Portal: func() any {
			return &PortalMetadata{}
		},
		Message: func() any {
			return &MessageMetadata{}
		},
		UserLogin: func() any {
			return &UserLoginMetadata{}
		},
		Ghost: func() any {
			return &GhostMetadata{}
		},
	}
}

func (oc *OpenAIConnector) LoadUserLogin(ctx context.Context, login *bridgev2.UserLogin) error {
	meta := loginMetadata(login)
	if strings.EqualFold(strings.TrimSpace(meta.Provider), ProviderCodex) {
		// Codex uses its own auth/tokens stored under CODEX_HOME. No OpenAI API key is required here.
		if oc.Config.Codex != nil && oc.Config.Codex.Enabled != nil && !*oc.Config.Codex.Enabled {
			return fmt.Errorf("codex integration is disabled in config")
		}

		oc.clientsMu.Lock()
		if existingAPI := oc.clients[login.ID]; existingAPI != nil {
			if existing, ok := existingAPI.(*CodexClient); ok {
				// Keep using one Codex client instance per login ID.
				existing.UserLogin = login
				login.Client = existing
				oc.clientsMu.Unlock()
				return nil
			}
			// Type mismatch: rebuild.
			delete(oc.clients, login.ID)
		}
		oc.clientsMu.Unlock()

		client, err := newCodexClient(login, oc)
		if err != nil {
			return err
		}
		oc.clientsMu.Lock()
		oc.clients[login.ID] = client
		oc.clientsMu.Unlock()
		login.Client = client
		return nil
	}

	key := strings.TrimSpace(oc.resolveProviderAPIKey(meta))
	if key == "" {
		return fmt.Errorf("no API key available for this login; please login again")
	}
	oc.clientsMu.Lock()
	if existingAPI := oc.clients[login.ID]; existingAPI != nil {
		existing, ok := existingAPI.(*AIClient)
		if !ok || existing == nil {
			// Type mismatch: rebuild.
			delete(oc.clients, login.ID)
			oc.clientsMu.Unlock()
			client, err := newAIClient(login, oc, key)
			if err != nil {
				return err
			}
			oc.clientsMu.Lock()
			oc.clients[login.ID] = client
			oc.clientsMu.Unlock()
			login.Client = client
			client.scheduleBootstrap()
			return nil
		}

		existingMeta := loginMetadata(existing.UserLogin)
		needsRebuild := existing.apiKey != key ||
			!strings.EqualFold(strings.TrimSpace(existingMeta.Provider), strings.TrimSpace(meta.Provider)) ||
			strings.TrimRight(strings.TrimSpace(existingMeta.BaseURL), "/") != strings.TrimRight(strings.TrimSpace(meta.BaseURL), "/")
		if needsRebuild {
			oc.clientsMu.Unlock()
			client, err := newAIClient(login, oc, key)
			if err != nil {
				return err
			}
			oc.clientsMu.Lock()
			oc.clients[login.ID] = client
			oc.clientsMu.Unlock()
			login.Client = client
			client.scheduleBootstrap()
			return nil
		}
		// Keep using one client instance per login ID when provider settings have not changed.
		existing.UserLogin = login
		login.Client = existing
		oc.clientsMu.Unlock()
		existing.scheduleBootstrap()
		return nil
	}
	oc.clientsMu.Unlock()

	client, err := newAIClient(login, oc, key)
	if err != nil {
		return err
	}
	oc.clientsMu.Lock()
	oc.clients[login.ID] = client
	oc.clientsMu.Unlock()
	login.Client = client
	client.scheduleBootstrap()
	return nil
}

// Package-level flow definitions (use Provider* constants as flow IDs)
func (oc *OpenAIConnector) GetLoginFlows() []bridgev2.LoginFlow {
	flows := []bridgev2.LoginFlow{
		{ID: ProviderBeeper, Name: "Beeper AI"},
		{ID: ProviderMagicProxy, Name: "Magic Proxy"},
		{ID: FlowCustom, Name: "Manual"},
	}
	if oc.Config.Codex != nil && oc.Config.Codex.Enabled != nil && *oc.Config.Codex.Enabled {
		flows = append(flows, bridgev2.LoginFlow{
			ID:          ProviderCodex,
			Name:        "Codex",
			Description: "Use a local Codex install via codex app-server (stdio).",
		})
	}
	return flows
}

func (oc *OpenAIConnector) CreateLogin(ctx context.Context, user *bridgev2.User, flowID string) (bridgev2.LoginProcess, error) {
	if flowID == ProviderCodex {
		if oc.Config.Codex != nil && oc.Config.Codex.Enabled != nil && !*oc.Config.Codex.Enabled {
			return nil, fmt.Errorf("login flow %s is not available", flowID)
		}
		return &CodexLogin{User: user, Connector: oc, FlowID: flowID}, nil
	}
	// Validate by checking if flowID is in available flows
	flows := oc.GetLoginFlows()
	valid := false
	for _, f := range flows {
		if f.ID == flowID {
			valid = true
			break
		}
	}
	if !valid {
		return nil, fmt.Errorf("login flow %s is not available", flowID)
	}
	return &OpenAILogin{User: user, Connector: oc, FlowID: flowID}, nil
}

// getLoginForPortal finds the correct user login based on the portal's Receiver.
// This ensures we use the correct provider/API credentials when a user has multiple accounts.
func (oc *OpenAIConnector) getLoginForPortal(ctx context.Context, user *bridgev2.User, portal *bridgev2.Portal) *bridgev2.UserLogin {
	if portal == nil {
		return user.GetDefaultLogin()
	}

	// The portal's Receiver field contains the UserLogin ID that owns this portal
	receiverID := portal.Receiver
	if receiverID == "" {
		oc.br.Log.Warn().Stringer("portal", portal.PortalKey).Msg("Portal has no receiver, using default login")
		return user.GetDefaultLogin()
	}

	// Get the specific login that matches the portal's receiver
	login, err := oc.br.GetExistingUserLoginByID(ctx, receiverID)
	if err != nil || login == nil {
		oc.br.Log.Warn().
			Err(err).
			Stringer("portal", portal.PortalKey).
			Str("receiver", string(receiverID)).
			Msg("Failed to get login for portal receiver, using default login")
		return user.GetDefaultLogin()
	}

	return login
}
