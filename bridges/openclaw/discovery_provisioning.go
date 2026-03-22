package openclaw

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/exhttp"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
)

type openClawDiscoveryProvisioningAPI struct {
	log       zerolog.Logger
	connector *OpenClawConnector
	prov      bridgev2.IProvisioningAPI
}

type openClawDiscoveryGatewayResponse struct {
	StableID                    string                        `json:"stable_id"`
	Source                      string                        `json:"source"`
	Domain                      string                        `json:"domain"`
	DisplayName                 string                        `json:"display_name"`
	GatewayURL                  string                        `json:"gateway_url"`
	ServiceHost                 string                        `json:"service_host,omitempty"`
	ServicePort                 int                           `json:"service_port,omitempty"`
	LanHost                     string                        `json:"lan_host,omitempty"`
	TailnetDNS                  string                        `json:"tailnet_dns,omitempty"`
	GatewayTLS                  bool                          `json:"gateway_tls,omitempty"`
	GatewayTLSFingerprintSHA256 string                        `json:"gateway_tls_fingerprint_sha256,omitempty"`
	SSHPort                     int                           `json:"ssh_port,omitempty"`
	CLIPath                     string                        `json:"cli_path,omitempty"`
	FlowID                      string                        `json:"flow_id"`
	FlowExpiresAtMS             int64                         `json:"flow_expires_at_ms"`
	LoginPrefill                openClawDiscoveryLoginPrefill `json:"login_prefill"`
}

type openClawDiscoveryLoginPrefill struct {
	URL   string `json:"url"`
	Label string `json:"label,omitempty"`
}

func (oc *OpenClawConnector) initProvisioning() {
	c, ok := oc.br.Matrix.(bridgev2.MatrixConnectorWithProvisioning)
	if !ok {
		return
	}
	prov := c.GetProvisioning()
	r := prov.GetRouter()
	if r == nil {
		return
	}
	api := &openClawDiscoveryProvisioningAPI{
		log:       oc.br.Log.With().Str("component", "provisioning").Str("bridge", "openclaw").Logger(),
		connector: oc,
		prov:      prov,
	}
	r.HandleFunc("GET /v1/discovery/gateways", api.handleListDiscoveredGateways)
}

func (oc *OpenClawConnector) discoveryEnabled() bool {
	return oc == nil || oc.Config.OpenClaw.Discovery.Enabled == nil || *oc.Config.OpenClaw.Discovery.Enabled
}

func (api *openClawDiscoveryProvisioningAPI) handleListDiscoveredGateways(w http.ResponseWriter, r *http.Request) {
	if api == nil || api.connector == nil || !api.connector.discoveryEnabled() {
		mautrix.MForbidden.WithMessage("OpenClaw discovery is disabled.").Write(w)
		return
	}
	user := api.prov.GetUser(r)
	if user == nil {
		mautrix.MForbidden.WithMessage("Missing provisioning user context.").Write(w)
		return
	}
	opts, err := api.discoveryOptions(r)
	if err != nil {
		mautrix.MInvalidParam.WithMessage("%s", err).Write(w)
		return
	}
	gateways, err := discoverOpenClawGateways(r.Context(), opts)
	if err != nil {
		mautrix.MUnknown.WithMessage("Couldn't discover gateways: %v.", err).Write(w)
		return
	}
	items := make([]openClawDiscoveryGatewayResponse, 0, len(gateways))
	for _, gateway := range gateways {
		flowID, expiresAt := api.connector.registerLoginPrefill(user, gateway.GatewayURL, gateway.DisplayName)
		items = append(items, openClawDiscoveryGatewayResponse{
			StableID:                    gateway.StableID,
			Source:                      gateway.Source,
			Domain:                      gateway.Domain,
			DisplayName:                 gateway.DisplayName,
			GatewayURL:                  gateway.GatewayURL,
			ServiceHost:                 gateway.ServiceHost,
			ServicePort:                 gateway.ServicePort,
			LanHost:                     gateway.LanHost,
			TailnetDNS:                  gateway.TailnetDNS,
			GatewayTLS:                  gateway.GatewayTLS,
			GatewayTLSFingerprintSHA256: gateway.GatewayTLSFingerprintSHA256,
			SSHPort:                     gateway.SSHPort,
			CLIPath:                     gateway.CLIPath,
			FlowID:                      flowID,
			FlowExpiresAtMS:             expiresAt.UnixMilli(),
			LoginPrefill: openClawDiscoveryLoginPrefill{
				URL:   gateway.GatewayURL,
				Label: gateway.DisplayName,
			},
		})
	}
	exhttp.WriteJSONResponse(w, http.StatusOK, map[string]any{"gateways": items})
}

func (api *openClawDiscoveryProvisioningAPI) discoveryOptions(r *http.Request) (openClawDiscoveryOptions, error) {
	timeout := time.Duration(api.connector.Config.OpenClaw.Discovery.TimeoutMS) * time.Millisecond
	if raw := strings.TrimSpace(r.URL.Query().Get("timeout_ms")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value <= 0 {
			return openClawDiscoveryOptions{}, errors.New("timeout_ms must be a positive integer")
		}
		if value > 10_000 {
			value = 10_000
		}
		timeout = time.Duration(value) * time.Millisecond
	}
	mode := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("wide_area")))
	wideAreaDomain := strings.TrimSpace(api.connector.Config.OpenClaw.Discovery.WideAreaDomain)
	switch mode {
	case "", "auto":
		return openClawDiscoveryOptions{
			Timeout:         timeout,
			WideAreaEnabled: wideAreaDomain != "",
			WideAreaDomain:  wideAreaDomain,
		}, nil
	case "off", "false", "0":
		return openClawDiscoveryOptions{Timeout: timeout}, nil
	case "on", "true", "1":
		if wideAreaDomain == "" {
			return openClawDiscoveryOptions{}, errWideAreaDomainRequired
		}
		return openClawDiscoveryOptions{
			Timeout:         timeout,
			WideAreaEnabled: true,
			WideAreaDomain:  wideAreaDomain,
		}, nil
	default:
		return openClawDiscoveryOptions{}, errors.New("invalid wide_area mode")
	}
}
