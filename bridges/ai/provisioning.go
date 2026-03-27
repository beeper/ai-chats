package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/exhttp"
	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"

	"github.com/beeper/agentremote/pkg/agents"
	"github.com/beeper/agentremote/pkg/agents/toolpolicy"
)

// ProvisioningAPI handles login-scoped profile, agent, and MCP configuration.
type ProvisioningAPI struct {
	log       zerolog.Logger
	connector *OpenAIConnector
	prov      bridgev2.IProvisioningAPI
}

// initProvisioning sets up the provisioning API endpoints.
func (oc *OpenAIConnector) initProvisioning() {
	c, ok := oc.br.Matrix.(bridgev2.MatrixConnectorWithProvisioning)
	if !ok {
		return
	}
	prov := c.GetProvisioning()
	r := prov.GetRouter()
	if r == nil {
		return
	}

	api := &ProvisioningAPI{
		log:       oc.br.Log.With().Str("component", "provisioning").Logger(),
		connector: oc,
		prov:      prov,
	}

	r.HandleFunc("GET /v1/models", api.handleListModels)
	r.HandleFunc("GET /v1/profile", api.handleGetProfile)
	r.HandleFunc("PUT /v1/profile", api.handlePutProfile)
	r.HandleFunc("GET /v1/agents", api.handleListAgents)
	r.HandleFunc("POST /v1/agents", api.handleCreateAgent)
	r.HandleFunc("GET /v1/agents/{agent_id}", api.handleGetAgent)
	r.HandleFunc("PUT /v1/agents/{agent_id}", api.handleUpdateAgent)
	r.HandleFunc("DELETE /v1/agents/{agent_id}", api.handleDeleteAgent)
	r.HandleFunc("GET /v1/mcp/servers", api.handleListMCPServers)
	r.HandleFunc("POST /v1/mcp/servers", api.handleCreateMCPServer)
	r.HandleFunc("PUT /v1/mcp/servers/{name}", api.handleUpdateMCPServer)
	r.HandleFunc("DELETE /v1/mcp/servers/{name}", api.handleDeleteMCPServer)
	r.HandleFunc("POST /v1/mcp/servers/{name}/connect", api.handleConnectMCPServer)
	r.HandleFunc("POST /v1/mcp/servers/{name}/disconnect", api.handleDisconnectMCPServer)

	oc.br.Log.Info().Msg("Registered provisioning API endpoints for AI profile, agents, and MCP")
}

// getLogin gets the default user login from the request.
func (api *ProvisioningAPI) getLogin(w http.ResponseWriter, r *http.Request) *bridgev2.UserLogin {
	user := api.prov.GetUser(r)
	if user == nil {
		mautrix.MNotFound.WithMessage("No logins found.").Write(w)
		return nil
	}
	login := user.GetDefaultLogin()
	if login == nil {
		mautrix.MNotFound.WithMessage("No logins found.").Write(w)
		return nil
	}
	return login
}

func (api *ProvisioningAPI) getClient(w http.ResponseWriter, r *http.Request) (*bridgev2.UserLogin, *AIClient) {
	login := api.getLogin(w, r)
	if login == nil {
		return nil, nil
	}
	client, ok := login.Client.(*AIClient)
	if !ok || client == nil {
		mautrix.MUnknown.WithMessage("Invalid AI client for login.").Write(w)
		return nil, nil
	}
	return login, client
}

// handleListModels handles GET /v1/models.
func (api *ProvisioningAPI) handleListModels(w http.ResponseWriter, r *http.Request) {
	_, client := api.getClient(w, r)
	if client == nil {
		return
	}
	models, err := client.listAvailableModels(r.Context(), false)
	if err != nil {
		mautrix.MUnknown.WithMessage("Couldn't list models: %v.", err).Write(w)
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, map[string]any{"models": models})
}

type profilePayload struct {
	Name               *string `json:"name,omitempty"`
	Occupation         *string `json:"occupation,omitempty"`
	AboutUser          *string `json:"about_user,omitempty"`
	CustomInstructions *string `json:"custom_instructions,omitempty"`
	Timezone           *string `json:"timezone,omitempty"`
}

type profileResponse struct {
	Name               string `json:"name,omitempty"`
	Occupation         string `json:"occupation,omitempty"`
	AboutUser          string `json:"about_user,omitempty"`
	CustomInstructions string `json:"custom_instructions,omitempty"`
	Timezone           string `json:"timezone,omitempty"`
}

func profileResponseFromMeta(meta *UserLoginMetadata) profileResponse {
	var resp profileResponse
	if meta == nil {
		return resp
	}
	if meta.Profile != nil {
		resp.Name = meta.Profile.Name
		resp.Occupation = meta.Profile.Occupation
		resp.AboutUser = meta.Profile.AboutUser
		resp.CustomInstructions = meta.Profile.CustomInstructions
	}
	resp.Timezone = meta.Timezone
	return resp
}

func applyProfilePayload(meta *UserLoginMetadata, payload profilePayload) error {
	if meta == nil {
		return errors.New("missing metadata")
	}
	if payload.Name != nil || payload.Occupation != nil || payload.AboutUser != nil || payload.CustomInstructions != nil {
		if meta.Profile == nil {
			meta.Profile = &UserProfile{}
		}
		if payload.Name != nil {
			meta.Profile.Name = strings.TrimSpace(*payload.Name)
		}
		if payload.Occupation != nil {
			meta.Profile.Occupation = strings.TrimSpace(*payload.Occupation)
		}
		if payload.AboutUser != nil {
			meta.Profile.AboutUser = strings.TrimSpace(*payload.AboutUser)
		}
		if payload.CustomInstructions != nil {
			meta.Profile.CustomInstructions = strings.TrimSpace(*payload.CustomInstructions)
		}
		if meta.Profile.Name == "" && meta.Profile.Occupation == "" && meta.Profile.AboutUser == "" && meta.Profile.CustomInstructions == "" {
			meta.Profile = nil
		}
	}
	if payload.Timezone != nil {
		tz := strings.TrimSpace(*payload.Timezone)
		if tz != "" {
			if _, err := time.LoadLocation(tz); err != nil {
				return fmt.Errorf("invalid timezone: %w", err)
			}
		}
		meta.Timezone = tz
	}
	return nil
}

// handleGetProfile handles GET /v1/profile.
func (api *ProvisioningAPI) handleGetProfile(w http.ResponseWriter, r *http.Request) {
	login := api.getLogin(w, r)
	if login == nil {
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, profileResponseFromMeta(loginMetadata(login)))
}

// handlePutProfile handles PUT /v1/profile.
func (api *ProvisioningAPI) handlePutProfile(w http.ResponseWriter, r *http.Request) {
	login := api.getLogin(w, r)
	if login == nil {
		return
	}
	var req profilePayload
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mautrix.MBadJSON.WithMessage("Invalid JSON: %v.", err).Write(w)
		return
	}
	meta := loginMetadata(login)
	if err := applyProfilePayload(meta, req); err != nil {
		mautrix.MInvalidParam.WithMessage("%v.", err).Write(w)
		return
	}
	if err := login.Save(r.Context()); err != nil {
		mautrix.MUnknown.WithMessage("Couldn't save changes: %v.", err).Write(w)
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, profileResponseFromMeta(meta))
}

type agentUpsertRequest struct {
	ID              string                       `json:"id,omitempty"`
	Name            string                       `json:"name,omitempty"`
	Description     string                       `json:"description,omitempty"`
	AvatarURL       string                       `json:"avatar_url,omitempty"`
	Model           string                       `json:"model,omitempty"`
	ModelFallback   []string                     `json:"model_fallback,omitempty"`
	SystemPrompt    string                       `json:"system_prompt,omitempty"`
	PromptMode      string                       `json:"prompt_mode,omitempty"`
	Tools           *toolpolicy.ToolPolicyConfig `json:"tools,omitempty"`
	Temperature     *float64                     `json:"temperature,omitempty"`
	ReasoningEffort string                       `json:"reasoning_effort,omitempty"`
	IdentityName    string                       `json:"identity_name,omitempty"`
	IdentityPersona string                       `json:"identity_persona,omitempty"`
	HeartbeatPrompt string                       `json:"heartbeat_prompt,omitempty"`
	MemorySearch    any                          `json:"memory_search,omitempty"`
}

func writeAgentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agents.ErrAgentNotFound):
		mautrix.MNotFound.WithMessage("Agent not found.").Write(w)
	case errors.Is(err, agents.ErrAgentIsPreset):
		mautrix.MForbidden.WithMessage("Preset agents can't be modified.").Write(w)
	case errors.Is(err, agents.ErrMissingAgentID), errors.Is(err, agents.ErrMissingAgentName):
		mautrix.MInvalidParam.WithMessage("%v.", err).Write(w)
	default:
		mautrix.MUnknown.WithMessage("Couldn't process agent: %v.", err).Write(w)
	}
}

func normalizeAgentUpsertRequest(req agentUpsertRequest, pathID string) *agents.AgentDefinition {
	agentID := strings.TrimSpace(pathID)
	if agentID == "" {
		agentID = strings.TrimSpace(req.ID)
	}
	if agentID == "" {
		agentID = uuid.NewString()
	}
	content := &AgentDefinitionContent{
		ID:              agentID,
		Name:            strings.TrimSpace(req.Name),
		Description:     strings.TrimSpace(req.Description),
		AvatarURL:       strings.TrimSpace(req.AvatarURL),
		Model:           strings.TrimSpace(req.Model),
		ModelFallback:   normalizeStringList(req.ModelFallback),
		SystemPrompt:    strings.TrimSpace(req.SystemPrompt),
		PromptMode:      strings.TrimSpace(req.PromptMode),
		Temperature:     ptr.Clone(req.Temperature),
		ReasoningEffort: strings.TrimSpace(req.ReasoningEffort),
		IdentityName:    strings.TrimSpace(req.IdentityName),
		IdentityPersona: strings.TrimSpace(req.IdentityPersona),
		HeartbeatPrompt: strings.TrimSpace(req.HeartbeatPrompt),
		MemorySearch:    req.MemorySearch,
	}
	content.Tools = req.Tools
	return FromAgentDefinitionContent(content)
}

func normalizeStringList(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	out := make([]string, 0, len(input))
	for _, item := range input {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func validateAgentModels(ctx context.Context, client *AIClient, agent *agents.AgentDefinition) error {
	if agent == nil || client == nil {
		return nil
	}
	models := []string{}
	if strings.TrimSpace(agent.Model.Primary) != "" {
		models = append(models, strings.TrimSpace(agent.Model.Primary))
	}
	models = append(models, normalizeStringList(agent.Model.Fallbacks)...)
	for _, model := range models {
		resolved, valid, err := client.resolveModelID(ctx, model)
		if err != nil {
			return err
		}
		if !valid || resolved == "" {
			return fmt.Errorf("invalid model: %s", model)
		}
		if model == agent.Model.Primary {
			agent.Model.Primary = resolved
			continue
		}
	}
	if len(agent.Model.Fallbacks) > 0 {
		resolvedFallbacks := make([]string, 0, len(agent.Model.Fallbacks))
		for _, fallback := range normalizeStringList(agent.Model.Fallbacks) {
			resolved, valid, err := client.resolveModelID(ctx, fallback)
			if err != nil {
				return err
			}
			if !valid || resolved == "" {
				return fmt.Errorf("invalid model: %s", fallback)
			}
			resolvedFallbacks = append(resolvedFallbacks, resolved)
		}
		agent.Model.Fallbacks = resolvedFallbacks
	}
	return nil
}

func agentResponse(agent *agents.AgentDefinition) *AgentDefinitionContent {
	if agent == nil {
		return nil
	}
	return ToAgentDefinitionContent(agent)
}

func listAgentsForResponse(ctx context.Context, store *AgentStoreAdapter) ([]*AgentDefinitionContent, error) {
	loaded, err := store.LoadAgents(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(loaded))
	for id := range loaded {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	out := make([]*AgentDefinitionContent, 0, len(ids))
	for _, id := range ids {
		if agent := loaded[id]; agent != nil {
			out = append(out, agentResponse(agent))
		}
	}
	return out, nil
}

func (api *ProvisioningAPI) handleListAgents(w http.ResponseWriter, r *http.Request) {
	_, client := api.getClient(w, r)
	if client == nil {
		return
	}
	items, err := listAgentsForResponse(r.Context(), NewAgentStoreAdapter(client))
	if err != nil {
		mautrix.MUnknown.WithMessage("Couldn't list agents: %v.", err).Write(w)
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, map[string]any{"agents": items})
}

func (api *ProvisioningAPI) handleGetAgent(w http.ResponseWriter, r *http.Request) {
	_, client := api.getClient(w, r)
	if client == nil {
		return
	}
	agentID := strings.TrimSpace(r.PathValue("agent_id"))
	agent, err := NewAgentStoreAdapter(client).GetAgentByID(r.Context(), agentID)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, agentResponse(agent))
}

func (api *ProvisioningAPI) handleCreateAgent(w http.ResponseWriter, r *http.Request) {
	_, client := api.getClient(w, r)
	if client == nil {
		return
	}
	var req agentUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mautrix.MBadJSON.WithMessage("Invalid JSON: %v.", err).Write(w)
		return
	}
	agent := normalizeAgentUpsertRequest(req, "")
	var err error
	if err = validateAgentModels(r.Context(), client, agent); err != nil {
		mautrix.MInvalidParam.WithMessage("%v.", err).Write(w)
		return
	}
	store := NewAgentStoreAdapter(client)
	if existing, err := store.GetAgentByID(r.Context(), agent.ID); err == nil && existing != nil {
		mautrix.MInvalidParam.WithMessage("Agent %s already exists.", agent.ID).Write(w)
		return
	}
	if err = store.SaveAgent(r.Context(), agent); err != nil {
		writeAgentError(w, err)
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusCreated, agentResponse(agent))
}

func (api *ProvisioningAPI) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	_, client := api.getClient(w, r)
	if client == nil {
		return
	}
	var req agentUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mautrix.MBadJSON.WithMessage("Invalid JSON: %v.", err).Write(w)
		return
	}
	agentID := strings.TrimSpace(r.PathValue("agent_id"))
	agent := normalizeAgentUpsertRequest(req, agentID)
	var err error
	if err = validateAgentModels(r.Context(), client, agent); err != nil {
		mautrix.MInvalidParam.WithMessage("%v.", err).Write(w)
		return
	}
	store := NewAgentStoreAdapter(client)
	existing, err := store.GetAgentByID(r.Context(), agentID)
	if err != nil {
		writeAgentError(w, err)
		return
	}
	if existing != nil && existing.IsPreset {
		writeAgentError(w, agents.ErrAgentIsPreset)
		return
	}
	if err = store.SaveAgent(r.Context(), agent); err != nil {
		writeAgentError(w, err)
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, agentResponse(agent))
}

func (api *ProvisioningAPI) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	_, client := api.getClient(w, r)
	if client == nil {
		return
	}
	agentID := strings.TrimSpace(r.PathValue("agent_id"))
	if err := NewAgentStoreAdapter(client).DeleteAgent(r.Context(), agentID); err != nil {
		writeAgentError(w, err)
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, map[string]any{"deleted": true})
}

type mcpServerUpsertRequest struct {
	Name      string   `json:"name,omitempty"`
	Transport string   `json:"transport,omitempty"`
	Endpoint  string   `json:"endpoint,omitempty"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	AuthType  string   `json:"auth_type,omitempty"`
	Token     string   `json:"token,omitempty"`
	AuthURL   string   `json:"auth_url,omitempty"`
	Kind      string   `json:"kind,omitempty"`
}

type mcpConnectRequest struct {
	Token string `json:"token,omitempty"`
}

type mcpServerResponse struct {
	Name      string   `json:"name"`
	Source    string   `json:"source,omitempty"`
	Transport string   `json:"transport,omitempty"`
	Endpoint  string   `json:"endpoint,omitempty"`
	Command   string   `json:"command,omitempty"`
	Args      []string `json:"args,omitempty"`
	AuthType  string   `json:"auth_type,omitempty"`
	TokenSet  bool     `json:"token_set,omitempty"`
	AuthURL   string   `json:"auth_url,omitempty"`
	Connected bool     `json:"connected,omitempty"`
	Kind      string   `json:"kind,omitempty"`
}

func mcpServerResponseFromNamed(server namedMCPServer) mcpServerResponse {
	cfg := normalizeMCPServerConfig(server.Config)
	return mcpServerResponse{
		Name:      server.Name,
		Source:    server.Source,
		Transport: cfg.Transport,
		Endpoint:  cfg.Endpoint,
		Command:   cfg.Command,
		Args:      slices.Clone(cfg.Args),
		AuthType:  cfg.AuthType,
		TokenSet:  cfg.Token != "" || cfg.AuthType == "none",
		AuthURL:   cfg.AuthURL,
		Connected: cfg.Connected,
		Kind:      cfg.Kind,
	}
}

func normalizeMCPRequest(req mcpServerUpsertRequest, pathName string) (string, MCPServerConfig, error) {
	name := ""
	if strings.TrimSpace(pathName) != "" {
		name = normalizeMCPServerName(pathName)
	}
	if name == "" {
		name = normalizeMCPServerName(req.Name)
	}
	if name == "" {
		return "", MCPServerConfig{}, errors.New("server name is required")
	}
	cfg := normalizeMCPServerConfig(MCPServerConfig{
		Transport: strings.TrimSpace(req.Transport),
		Endpoint:  strings.TrimSpace(req.Endpoint),
		Command:   strings.TrimSpace(req.Command),
		Args:      normalizeStringList(req.Args),
		AuthType:  strings.TrimSpace(req.AuthType),
		Token:     strings.TrimSpace(req.Token),
		AuthURL:   strings.TrimSpace(req.AuthURL),
		Kind:      strings.TrimSpace(req.Kind),
		Connected: false,
	})
	if !mcpServerHasTarget(cfg) {
		return "", MCPServerConfig{}, errors.New("mcp server target is required")
	}
	return name, cfg, nil
}

func validateMCPConfig(client *AIClient, cfg MCPServerConfig) error {
	if mcpServerUsesStdio(cfg) && !client.isMCPStdioEnabled() {
		return errors.New("stdio MCP servers are disabled")
	}
	if cfg.Transport == mcpTransportStreamableHTTP && !isLikelyHTTPURL(cfg.Endpoint) {
		return errors.New("invalid MCP endpoint")
	}
	return nil
}

func resolveNamedMCPServer(client *AIClient, name string) (namedMCPServer, error) {
	target, _, err := resolveMCPServerArg(client, []string{name})
	return target, err
}

func ensureLoginMCPServer(meta *UserLoginMetadata) {
	if meta.ServiceTokens == nil {
		meta.ServiceTokens = &ServiceTokens{}
	}
	if meta.ServiceTokens.MCPServers == nil {
		meta.ServiceTokens.MCPServers = map[string]MCPServerConfig{}
	}
}

func (api *ProvisioningAPI) handleListMCPServers(w http.ResponseWriter, r *http.Request) {
	_, client := api.getClient(w, r)
	if client == nil {
		return
	}
	servers := client.configuredMCPServers()
	items := make([]mcpServerResponse, 0, len(servers))
	for _, server := range servers {
		items = append(items, mcpServerResponseFromNamed(server))
	}
	slices.SortFunc(items, func(a, b mcpServerResponse) int { return strings.Compare(a.Name, b.Name) })
	exhttp.WriteJSONResponse(w, http.StatusOK, map[string]any{"servers": items})
}

func (api *ProvisioningAPI) handleCreateMCPServer(w http.ResponseWriter, r *http.Request) {
	login, client := api.getClient(w, r)
	if client == nil {
		return
	}
	var req mcpServerUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mautrix.MBadJSON.WithMessage("Invalid JSON: %v.", err).Write(w)
		return
	}
	name, cfg, err := normalizeMCPRequest(req, "")
	if err != nil {
		mautrix.MInvalidParam.WithMessage("%v.", err).Write(w)
		return
	}
	if err = validateMCPConfig(client, cfg); err != nil {
		mautrix.MInvalidParam.WithMessage("%v.", err).Write(w)
		return
	}
	meta := loginMetadata(login)
	ensureLoginMCPServer(meta)
	if _, exists := meta.ServiceTokens.MCPServers[name]; exists {
		mautrix.MInvalidParam.WithMessage("MCP server %s already exists.", name).Write(w)
		return
	}
	setLoginMCPServer(meta, name, cfg)
	if err = login.Save(r.Context()); err != nil {
		mautrix.MUnknown.WithMessage("Couldn't save MCP server: %v.", err).Write(w)
		return
	}
	client.invalidateMCPToolCache()
	exhttp.WriteJSONResponse(w, http.StatusCreated, mcpServerResponseFromNamed(namedMCPServer{Name: name, Config: cfg, Source: "login"}))
}

func (api *ProvisioningAPI) handleUpdateMCPServer(w http.ResponseWriter, r *http.Request) {
	login, client := api.getClient(w, r)
	if client == nil {
		return
	}
	var req mcpServerUpsertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mautrix.MBadJSON.WithMessage("Invalid JSON: %v.", err).Write(w)
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	_, err := resolveNamedMCPServer(client, name)
	if err != nil && err.Error() != "not found" {
		mautrix.MInvalidParam.WithMessage("Couldn't resolve MCP server %s.", name).Write(w)
		return
	}
	resolvedName, cfg, err := normalizeMCPRequest(req, name)
	if err != nil {
		mautrix.MInvalidParam.WithMessage("%v.", err).Write(w)
		return
	}
	if err = validateMCPConfig(client, cfg); err != nil {
		mautrix.MInvalidParam.WithMessage("%v.", err).Write(w)
		return
	}
	meta := loginMetadata(login)
	setLoginMCPServer(meta, resolvedName, cfg)
	if err = login.Save(r.Context()); err != nil {
		mautrix.MUnknown.WithMessage("Couldn't save MCP server: %v.", err).Write(w)
		return
	}
	client.invalidateMCPToolCache()
	exhttp.WriteJSONResponse(w, http.StatusOK, mcpServerResponseFromNamed(namedMCPServer{Name: resolvedName, Config: cfg, Source: "login"}))
}

func (api *ProvisioningAPI) handleDeleteMCPServer(w http.ResponseWriter, r *http.Request) {
	login, client := api.getClient(w, r)
	if client == nil {
		return
	}
	name := strings.TrimSpace(r.PathValue("name"))
	target, err := resolveNamedMCPServer(client, name)
	if err != nil {
		mautrix.MNotFound.WithMessage("MCP server not found.").Write(w)
		return
	}
	loginServers := client.loginMCPServers()
	if _, ok := loginServers[target.Name]; !ok {
		mautrix.MForbidden.WithMessage("Config-managed MCP servers can't be deleted here.").Write(w)
		return
	}
	meta := loginMetadata(login)
	clearLoginMCPServer(meta, target.Name)
	if err = login.Save(r.Context()); err != nil {
		mautrix.MUnknown.WithMessage("Couldn't remove MCP server: %v.", err).Write(w)
		return
	}
	client.invalidateMCPToolCache()
	exhttp.WriteJSONResponse(w, http.StatusOK, map[string]any{"deleted": true})
}

func connectMCPServer(ctx context.Context, client *AIClient, login *bridgev2.UserLogin, name string, tokenOverride string) (namedMCPServer, int, error) {
	target, err := resolveNamedMCPServer(client, name)
	if err != nil {
		return namedMCPServer{}, 0, err
	}
	cfg := normalizeMCPServerConfig(target.Config)
	if tokenOverride != "" && !mcpServerUsesStdio(cfg) {
		cfg.Token = strings.TrimSpace(tokenOverride)
		if cfg.Token != "" && cfg.AuthType == "none" {
			cfg.AuthType = "bearer"
		}
	}
	if !mcpServerHasTarget(cfg) {
		return namedMCPServer{}, 0, errors.New("mcp server target is required")
	}
	if mcpServerNeedsToken(cfg) && cfg.Token == "" {
		cfg.Connected = false
		setLoginMCPServer(loginMetadata(login), target.Name, cfg)
		if err = login.Save(ctx); err != nil {
			return namedMCPServer{}, 0, err
		}
		client.invalidateMCPToolCache()
		return namedMCPServer{Name: target.Name, Config: cfg, Source: "login"}, 0, errors.New("mcp server token is required")
	}
	cfg.Connected = true
	count, connectErr := client.verifyMCPServerConnection(ctx, namedMCPServer{Name: target.Name, Config: cfg, Source: "login"})
	if connectErr != nil {
		cfg.Connected = false
		setLoginMCPServer(loginMetadata(login), target.Name, cfg)
		if err = login.Save(ctx); err != nil {
			return namedMCPServer{}, 0, err
		}
		client.invalidateMCPToolCache()
		return namedMCPServer{Name: target.Name, Config: cfg, Source: "login"}, 0, connectErr
	}
	setLoginMCPServer(loginMetadata(login), target.Name, cfg)
	if err = login.Save(ctx); err != nil {
		return namedMCPServer{}, 0, err
	}
	client.invalidateMCPToolCache()
	return namedMCPServer{Name: target.Name, Config: cfg, Source: "login"}, count, nil
}

func (api *ProvisioningAPI) handleConnectMCPServer(w http.ResponseWriter, r *http.Request) {
	login, client := api.getClient(w, r)
	if client == nil {
		return
	}
	var req mcpConnectRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			mautrix.MBadJSON.WithMessage("Invalid JSON: %v.", err).Write(w)
			return
		}
	}
	server, count, err := connectMCPServer(r.Context(), client, login, strings.TrimSpace(r.PathValue("name")), strings.TrimSpace(req.Token))
	if err != nil {
		code := http.StatusBadRequest
		if mcpCallLikelyAuthError(err) {
			code = http.StatusUnauthorized
		} else if strings.Contains(err.Error(), "not found") {
			code = http.StatusNotFound
		}
		exhttp.WriteJSONResponse(w, code, map[string]any{
			"error":  err.Error(),
			"server": mcpServerResponseFromNamed(server),
		})
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, map[string]any{
		"server":     mcpServerResponseFromNamed(server),
		"tool_count": count,
	})
}

func (api *ProvisioningAPI) handleDisconnectMCPServer(w http.ResponseWriter, r *http.Request) {
	login, client := api.getClient(w, r)
	if client == nil {
		return
	}
	target, err := resolveNamedMCPServer(client, strings.TrimSpace(r.PathValue("name")))
	if err != nil {
		mautrix.MNotFound.WithMessage("MCP server not found.").Write(w)
		return
	}
	cfg := normalizeMCPServerConfig(target.Config)
	cfg.Connected = false
	setLoginMCPServer(loginMetadata(login), target.Name, cfg)
	if err = login.Save(r.Context()); err != nil {
		mautrix.MUnknown.WithMessage("Couldn't disconnect MCP server: %v.", err).Write(w)
		return
	}
	client.invalidateMCPToolCache()
	exhttp.WriteJSONResponse(w, http.StatusOK, mcpServerResponseFromNamed(namedMCPServer{Name: target.Name, Config: cfg, Source: "login"}))
}
