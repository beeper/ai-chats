package connector

import (
	"encoding/json"
	"net/http"

	"github.com/rs/zerolog"
	"go.mau.fi/util/exhttp"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
)

// ProvisioningAPI handles the provisioning endpoints for user defaults
type ProvisioningAPI struct {
	log       zerolog.Logger
	connector *OpenAIConnector
	prov      bridgev2.IProvisioningAPI
}

// initProvisioning sets up the provisioning API endpoints
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
	r.HandleFunc("GET /v1/defaults", api.handleGetDefaults)
	r.HandleFunc("PUT /v1/defaults", api.handleSetDefaults)

	oc.br.Log.Info().Msg("Registered provisioning API endpoints for user defaults")
}

// getLogin gets the user login from the request
func (api *ProvisioningAPI) getLogin(w http.ResponseWriter, r *http.Request) *bridgev2.UserLogin {
	user := api.prov.GetUser(r)
	logins := user.GetUserLogins()
	if len(logins) < 1 {
		mautrix.MNotFound.WithMessage("No logins found.").Write(w)
		return nil
	}
	return logins[0]
}

// handleListModels handles GET /v1/models
func (api *ProvisioningAPI) handleListModels(w http.ResponseWriter, r *http.Request) {
	login := api.getLogin(w, r)
	if login == nil {
		return
	}
	client := login.Client.(*AIClient)
	models, err := client.listAvailableModels(r.Context(), false)
	if err != nil {
		mautrix.MUnknown.WithMessage("Couldn't list models: %v.", err).Write(w)
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, map[string]any{"models": models})
}

// handleGetDefaults handles GET /v1/defaults
func (api *ProvisioningAPI) handleGetDefaults(w http.ResponseWriter, r *http.Request) {
	login := api.getLogin(w, r)
	if login == nil {
		return
	}
	meta := loginMetadata(login)
	resp := map[string]any{}
	if meta.Defaults != nil {
		if meta.Defaults.Model != "" {
			resp["model"] = meta.Defaults.Model
		}
		if meta.Defaults.SystemPrompt != "" {
			resp["system_prompt"] = meta.Defaults.SystemPrompt
		}
		if meta.Defaults.Temperature != nil {
			resp["temperature"] = meta.Defaults.Temperature
		}
		if meta.Defaults.ReasoningEffort != "" {
			resp["reasoning_effort"] = meta.Defaults.ReasoningEffort
		}
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, resp)
}

// ReqSetDefaults is the request body for PUT /v1/defaults
type ReqSetDefaults struct {
	Model           *string  `json:"model,omitempty"`
	SystemPrompt    *string  `json:"system_prompt,omitempty"`
	Temperature     *float64 `json:"temperature,omitempty"`
	ReasoningEffort *string  `json:"reasoning_effort,omitempty"`
}

// handleSetDefaults handles PUT /v1/defaults
func (api *ProvisioningAPI) handleSetDefaults(w http.ResponseWriter, r *http.Request) {
	login := api.getLogin(w, r)
	if login == nil {
		return
	}
	var req ReqSetDefaults
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mautrix.MBadJSON.WithMessage("Invalid JSON: %v.", err).Write(w)
		return
	}

	meta := loginMetadata(login)
	if meta.Defaults == nil {
		meta.Defaults = &UserDefaults{}
	}

	// Validate and apply model
	if req.Model != nil {
		client := login.Client.(*AIClient)
		if valid, _ := client.validateModel(r.Context(), *req.Model); !valid {
			mautrix.MInvalidParam.WithMessage("Invalid model: %s.", *req.Model).Write(w)
			return
		}
		meta.Defaults.Model = *req.Model
	}

	// Apply other settings
	if req.SystemPrompt != nil {
		meta.Defaults.SystemPrompt = *req.SystemPrompt
	}
	if req.Temperature != nil {
		if *req.Temperature < 0 || *req.Temperature > 2 {
			mautrix.MInvalidParam.WithMessage("Temperature must be between 0 and 2.").Write(w)
			return
		}
		meta.Defaults.Temperature = req.Temperature
	}
	if req.ReasoningEffort != nil {
		switch *req.ReasoningEffort {
		case "", "none", "low", "medium", "high", "xhigh":
			meta.Defaults.ReasoningEffort = *req.ReasoningEffort
		default:
			mautrix.MInvalidParam.WithMessage("reasoning_effort must be one of: none, low, medium, high, xhigh.").Write(w)
			return
		}
	}
	if err := login.Save(r.Context()); err != nil {
		mautrix.MUnknown.WithMessage("Couldn't save changes: %v.", err).Write(w)
		return
	}

	// Return updated defaults
	api.handleGetDefaults(w, r)
}
