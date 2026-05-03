package connector

import (
	"context"
	"fmt"
	"strings"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type ResponderKind string

const (
	ResponderKindModel ResponderKind = "model"
)

type ResponderInfo struct {
	Kind                ResponderKind
	GhostID             networkid.UserID
	ModelID             string
	DisplayName         string
	ContextLimit        int
	MaxOutputTokens     int
	SupportsReasoning   bool
	SupportsToolCalling bool
	SupportsImageGen    bool
	SupportsVision      bool
	SupportsAudio       bool
	SupportsVideo       bool
	SupportsPDF         bool
}

type ResponderResolveOptions struct {
	RuntimeModelOverride string
}

func (oc *AIClient) responderForMeta(ctx context.Context, meta *PortalMetadata) *ResponderInfo {
	opts := ResponderResolveOptions{}
	if meta != nil {
		opts.RuntimeModelOverride = strings.TrimSpace(meta.RuntimeModelOverride)
	}
	responder, err := oc.resolveResponder(ctx, meta, opts)
	if err == nil && responder != nil {
		return responder
	}
	modelID := oc.defaultModelForProvider()
	if meta != nil {
		if override := strings.TrimSpace(meta.RuntimeModelOverride); override != "" {
			modelID = override
		}
	}
	if modelID == "" {
		return nil
	}
	info := oc.responderModelInfo(modelID)
	ri := responderFromModelInfo(info)
	ri.Kind = ResponderKindModel
	ri.GhostID = modelUserID(modelID)
	ri.ModelID = modelID
	ri.DisplayName = strings.TrimSpace(modelContactName(modelID, info))
	return &ri
}

func (oc *AIClient) responderProvider(responder *ResponderInfo) string {
	if responder == nil {
		loginMeta := loginMetadata(oc.UserLogin)
		if loginMeta != nil {
			return strings.TrimSpace(loginMeta.Provider)
		}
		return ""
	}
	provider, _ := splitModelProvider(responder.ModelID)
	if provider != "" {
		return provider
	}
	loginMeta := loginMetadata(oc.UserLogin)
	if loginMeta != nil {
		return strings.TrimSpace(loginMeta.Provider)
	}
	return ""
}

func (oc *AIClient) resolveResponder(ctx context.Context, meta *PortalMetadata, opts ResponderResolveOptions) (*ResponderInfo, error) {
	override := strings.TrimSpace(opts.RuntimeModelOverride)
	var target *ResolvedTarget
	if meta != nil {
		target = meta.ResolvedTarget
	}
	if target == nil {
		target = &ResolvedTarget{
			Kind:    ResolvedTargetModel,
			ModelID: oc.defaultModelForProvider(),
		}
	}

	switch target.Kind {
	case ResolvedTargetModel, ResolvedTargetUnknown:
		modelID := strings.TrimSpace(target.ModelID)
		if override != "" {
			modelID = override
		}
		modelID = strings.TrimSpace(modelID)
		if modelID == "" {
			modelID = oc.defaultModelForProvider()
		}
		if modelID == "" {
			return nil, fmt.Errorf("model target missing model id")
		}
		info := oc.responderModelInfo(modelID)
		ghostID := target.GhostID
		if ghostID == "" || override != "" {
			ghostID = modelUserID(modelID)
		}
		ri := responderFromModelInfo(info)
		ri.Kind = ResponderKindModel
		ri.GhostID = ghostID
		ri.ModelID = modelID
		ri.DisplayName = strings.TrimSpace(modelContactName(modelID, info))
		return &ri, nil
	default:
		return nil, fmt.Errorf("unsupported target kind: %s", target.Kind)
	}
}

func responderFromModelInfo(info *ModelInfo) ResponderInfo {
	return ResponderInfo{
		ContextLimit:        responderContextLimit(info),
		MaxOutputTokens:     responderMaxOutputTokens(info),
		SupportsReasoning:   info != nil && info.SupportsReasoning,
		SupportsToolCalling: info != nil && info.SupportsToolCalling,
		SupportsImageGen:    info != nil && info.SupportsImageGen,
		SupportsVision:      info != nil && info.SupportsVision,
		SupportsAudio:       info != nil && info.SupportsAudio,
		SupportsVideo:       info != nil && info.SupportsVideo,
		SupportsPDF:         info != nil && info.SupportsPDF,
	}
}

func (oc *AIClient) responderModelInfo(modelID string) *ModelInfo {
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	return oc.findModelInfo(modelID)
}

func responderContextLimit(info *ModelInfo) int {
	if info != nil && info.ContextWindow > 0 {
		return info.ContextWindow
	}
	return 128000
}

func responderMaxOutputTokens(info *ModelInfo) int {
	if info != nil && info.MaxOutputTokens > 0 {
		return info.MaxOutputTokens
	}
	return 0
}

func (ri *ResponderInfo) ModelCapabilities() ModelCapabilities {
	if ri == nil {
		return ModelCapabilities{}
	}
	return ModelCapabilities{
		SupportsVision:      ri.SupportsVision,
		SupportsReasoning:   ri.SupportsReasoning,
		SupportsPDF:         ri.SupportsPDF,
		SupportsImageGen:    ri.SupportsImageGen,
		SupportsAudio:       ri.SupportsAudio,
		SupportsVideo:       ri.SupportsVideo,
		SupportsToolCalling: ri.SupportsToolCalling,
	}
}

func responderMetadataMap(responder *ResponderInfo) map[string]any {
	if responder == nil {
		return nil
	}
	metadata := map[string]any{
		"com.beeper.ai.model_id":      responder.ModelID,
		"com.beeper.ai.context_limit": responder.ContextLimit,
		"com.beeper.ai.capabilities":  responder.ModelCapabilities(),
	}
	return metadata
}

func responderExtraProfile(responder *ResponderInfo) database.ExtraProfile {
	var extra database.ExtraProfile
	for key, value := range responderMetadataMap(responder) {
		_ = extra.Set(key, value)
	}
	return extra
}

func responderUserInfo(responder *ResponderInfo, identifiers []string, isBot bool) *bridgev2.UserInfo {
	if responder == nil {
		return nil
	}
	return &bridgev2.UserInfo{
		Name:         ptr.NonZero(strings.TrimSpace(responder.DisplayName)),
		IsBot:        ptr.Ptr(isBot),
		Identifiers:  identifiers,
		ExtraProfile: responderExtraProfile(responder),
	}
}

func responderUserInfoOrDefault(responder *ResponderInfo, fallbackName string, identifiers []string, isBot bool) *bridgev2.UserInfo {
	if info := responderUserInfo(responder, identifiers, isBot); info != nil {
		return info
	}
	return &bridgev2.UserInfo{
		Name:        ptr.Ptr(fallbackName),
		IsBot:       ptr.Ptr(isBot),
		Identifiers: identifiers,
	}
}
