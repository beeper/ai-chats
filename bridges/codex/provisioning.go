package codex

import (
	"encoding/json"
	"net/http"

	"go.mau.fi/util/exhttp"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
)

type provisioningAPI struct {
	connector *CodexConnector
	prov      bridgev2.IProvisioningAPI
}

type workspaceRequest struct {
	Path string `json:"path"`
}

func (cc *CodexConnector) initProvisioning() {
	c, ok := cc.br.Matrix.(bridgev2.MatrixConnectorWithProvisioning)
	if !ok {
		return
	}
	prov := c.GetProvisioning()
	r := prov.GetRouter()
	if r == nil {
		return
	}
	api := &provisioningAPI{connector: cc, prov: prov}
	r.HandleFunc("GET /v1/codex/workspaces", api.handleListWorkspaces)
	r.HandleFunc("POST /v1/codex/workspaces", api.handleAddWorkspace)
	r.HandleFunc("DELETE /v1/codex/workspaces", api.handleRemoveWorkspace)
}

func (api *provisioningAPI) getClient(w http.ResponseWriter, r *http.Request) (*bridgev2.UserLogin, *CodexClient) {
	user := api.prov.GetUser(r)
	if user == nil {
		mautrix.MForbidden.WithMessage("Missing provisioning user context.").Write(w)
		return nil, nil
	}
	login := user.GetDefaultLogin()
	if login == nil {
		mautrix.MNotFound.WithMessage("No logins found.").Write(w)
		return nil, nil
	}
	client, ok := login.Client.(*CodexClient)
	if !ok || client == nil {
		mautrix.MUnknown.WithMessage("Invalid Codex client for login.").Write(w)
		return nil, nil
	}
	return login, client
}

func (api *provisioningAPI) decodeWorkspaceRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	var req workspaceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		mautrix.MBadJSON.WithMessage("Invalid JSON: %v.", err).Write(w)
		return "", false
	}
	path, err := resolveExistingDirectory(req.Path)
	if err != nil {
		mautrix.MInvalidParam.WithMessage("%v.", err).Write(w)
		return "", false
	}
	return path, true
}

func (api *provisioningAPI) handleListWorkspaces(w http.ResponseWriter, r *http.Request) {
	login, _ := api.getClient(w, r)
	if login == nil {
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, map[string]any{
		"workspaces": managedCodexPaths(loginMetadata(login)),
	})
}

func (api *provisioningAPI) handleAddWorkspace(w http.ResponseWriter, r *http.Request) {
	_, client := api.getClient(w, r)
	if client == nil {
		return
	}
	path, ok := api.decodeWorkspaceRequest(w, r)
	if !ok {
		return
	}
	added, err := client.trackWorkspace(r.Context(), path, "provisioning")
	if err != nil {
		mautrix.MUnknown.WithMessage("Couldn't add workspace: %v.", err).Write(w)
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, map[string]any{
		"path":  path,
		"added": added,
	})
}

func (api *provisioningAPI) handleRemoveWorkspace(w http.ResponseWriter, r *http.Request) {
	_, client := api.getClient(w, r)
	if client == nil {
		return
	}
	path, ok := api.decodeWorkspaceRequest(w, r)
	if !ok {
		return
	}
	removed, err := client.untrackWorkspace(r.Context(), path, "provisioning")
	if err != nil {
		mautrix.MUnknown.WithMessage("Couldn't remove workspace: %v.", err).Write(w)
		return
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, map[string]any{
		"path":    path,
		"removed": removed,
	})
}
