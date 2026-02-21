package connector

import (
	"context"
	"strings"
	"time"

	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"

	integrationmemory "github.com/beeper/ai-bridge/pkg/integrations/memory"
)

const memorySearchTimeout = 10 * time.Second

type memoryRuntimeAdapter struct {
	client *AIClient
}

func (a *memoryRuntimeAdapter) ResolveConfig(agentID string) (*integrationmemory.ResolvedConfig, error) {
	if a == nil || a.client == nil {
		return nil, nil
	}
	return resolveMemorySearchConfig(a.client, agentID)
}

func (a *memoryRuntimeAdapter) ResolveOpenAIEmbeddingConfig(cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	if a == nil {
		return "", "", nil
	}
	return resolveOpenAIEmbeddingConfig(a.client, cfg)
}

func (a *memoryRuntimeAdapter) ResolveDirectOpenAIEmbeddingConfig(cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	if a == nil {
		return "", "", nil
	}
	return resolveDirectOpenAIEmbeddingConfig(a.client, cfg)
}

func (a *memoryRuntimeAdapter) ResolveGeminiEmbeddingConfig(cfg *integrationmemory.ResolvedConfig) (string, string, map[string]string) {
	if a == nil {
		return "", "", nil
	}
	return resolveGeminiEmbeddingConfig(a.client, cfg)
}

func (a *memoryRuntimeAdapter) ResolvePromptWorkspaceDir() string {
	return resolvePromptWorkspaceDir()
}

func (a *memoryRuntimeAdapter) ListSessionPortals(ctx context.Context, loginID, agentID string) ([]integrationmemory.SessionPortal, error) {
	if a == nil || a.client == nil || a.client.UserLogin == nil || a.client.UserLogin.Bridge == nil || a.client.UserLogin.Bridge.DB == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(loginID) == "" {
		loginID = string(a.client.UserLogin.ID)
	}

	allowedShared := map[string]struct{}{}
	if ups, err := a.client.UserLogin.Bridge.DB.UserPortal.GetAllForLogin(ctx, a.client.UserLogin.UserLogin); err == nil {
		for _, up := range ups {
			if up == nil || up.Portal.Receiver != "" {
				continue
			}
			allowedShared[up.Portal.String()] = struct{}{}
		}
	}

	portals, err := a.client.UserLogin.Bridge.DB.Portal.GetAll(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]integrationmemory.SessionPortal, 0, len(portals))
	for _, portal := range portals {
		if portal == nil || portal.MXID == "" {
			continue
		}
		if portal.Receiver != "" && string(portal.Receiver) != loginID {
			continue
		}
		if portal.Receiver == "" && len(allowedShared) > 0 {
			if _, ok := allowedShared[portal.PortalKey.String()]; !ok {
				continue
			}
		}
		meta, ok := portal.Metadata.(*PortalMetadata)
		if !ok || meta == nil || meta.IsCronRoom {
			continue
		}
		if resolveAgentID(meta) != agentID {
			continue
		}
		key := portal.PortalKey.String()
		if key == "" {
			continue
		}
		out = append(out, integrationmemory.SessionPortal{Key: key, PortalKey: portal.PortalKey})
	}
	return out, nil
}

func (a *memoryRuntimeAdapter) BridgeDB() *dbutil.Database {
	if a == nil || a.client == nil {
		return nil
	}
	return a.client.bridgeDB()
}

func (a *memoryRuntimeAdapter) BridgeID() string {
	if a == nil || a.client == nil || a.client.UserLogin == nil || a.client.UserLogin.Bridge == nil || a.client.UserLogin.Bridge.DB == nil {
		return ""
	}
	return string(a.client.UserLogin.Bridge.DB.BridgeID)
}

func (a *memoryRuntimeAdapter) LoginID() string {
	if a == nil || a.client == nil || a.client.UserLogin == nil {
		return ""
	}
	return string(a.client.UserLogin.ID)
}

func (a *memoryRuntimeAdapter) Logger() zerolog.Logger {
	if a == nil || a.client == nil {
		return zerolog.Logger{}
	}
	return a.client.log
}

func (oc *AIClient) getMemoryManager(agentID string) (*integrationmemory.MemorySearchManager, string) {
	if oc == nil {
		return nil, "memory search unavailable"
	}
	manager, errMsg := integrationmemory.GetMemorySearchManager(&memoryRuntimeAdapter{client: oc}, agentID)
	if manager == nil {
		if errMsg == "" {
			errMsg = "memory search unavailable"
		}
		return nil, errMsg
	}
	return manager, ""
}
