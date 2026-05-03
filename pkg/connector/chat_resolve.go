package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

type chatResolveTarget struct {
	modelID       string
	modelRedirect networkid.UserID
	response      *bridgev2.ResolveIdentifierResponse
}

func modelRedirectTarget(requested, resolved string) networkid.UserID {
	requested = strings.TrimSpace(requested)
	resolved = strings.TrimSpace(resolved)
	if requested == "" || resolved == "" || requested == resolved {
		return ""
	}
	return modelUserID(resolved)
}

func parseChatGhostTarget(ghostID string) string {
	return parseModelFromGhostID(ghostID)
}

func normalizeChatIdentifier(identifier string) string {
	id := strings.TrimSpace(identifier)
	if canonicalModelID := parseCanonicalModelIdentifier(id); canonicalModelID != "" {
		return canonicalModelID
	}
	return id
}

func (oc *AIClient) resolveModelChatTarget(ctx context.Context, identifier string) (*chatResolveTarget, error) {
	resolved, valid, err := oc.resolveModelID(ctx, identifier)
	if err != nil {
		return nil, err
	}
	if !valid || resolved == "" {
		return nil, nil
	}
	return &chatResolveTarget{
		modelID:       resolved,
		modelRedirect: modelRedirectTarget(identifier, resolved),
	}, nil
}

func (oc *AIClient) resolveParsedChatGhostTarget(ctx context.Context, modelID string) (*chatResolveTarget, bool, error) {
	if modelID == "" {
		return nil, false, nil
	}
	target, err := oc.resolveModelChatTarget(ctx, modelID)
	if err != nil {
		return nil, true, err
	}
	if target == nil {
		return nil, true, bridgev2.WrapRespErr(fmt.Errorf("model '%s' not found", modelID), mautrix.MNotFound)
	}
	return target, true, nil
}

func (oc *AIClient) resolveChatTargetFromIdentifier(ctx context.Context, identifier string) (*chatResolveTarget, error) {
	id := normalizeChatIdentifier(identifier)
	if id == "" {
		return nil, bridgev2.WrapRespErr(errors.New("identifier is required"), mautrix.MInvalidParam)
	}
	modelID := parseChatGhostTarget(id)
	if target, resolved, err := oc.resolveParsedChatGhostTarget(ctx, modelID); resolved {
		if err != nil {
			return nil, err
		}
		return target, nil
	}
	target, err := oc.resolveModelChatTarget(ctx, id)
	if err != nil {
		return nil, err
	}
	if target != nil {
		return target, nil
	}
	return nil, bridgev2.WrapRespErr(fmt.Errorf("identifier '%s' not found", id), mautrix.MNotFound)
}

func (oc *AIClient) resolveChatTargetFromGhost(ctx context.Context, ghost *bridgev2.Ghost) (*chatResolveTarget, error) {
	if ghost == nil {
		return nil, bridgev2.WrapRespErr(errors.New("ghost is required"), mautrix.MInvalidParam)
	}
	ghostID := string(ghost.ID)
	modelID := parseChatGhostTarget(ghostID)
	if target, resolved, err := oc.resolveParsedChatGhostTarget(ctx, modelID); resolved {
		if err != nil {
			return nil, err
		}
		return target, nil
	}
	return nil, bridgev2.WrapRespErr(fmt.Errorf("unsupported ghost ID: %s", ghostID), mautrix.MInvalidParam)
}

func (oc *AIClient) resolveChatTargetResponse(ctx context.Context, target *chatResolveTarget, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	if target == nil {
		return nil, bridgev2.WrapRespErr(errors.New("identifier target is required"), mautrix.MInvalidParam)
	}
	if target.response != nil {
		return target.response, nil
	}
	switch {
	case target.modelID != "":
		modelID := target.modelID
		userID := modelUserID(modelID)
		ghost, err := oc.resolveChatGhost(ctx, userID)
		if err != nil {
			return nil, err
		}

		oc.ensureGhostDisplayName(ctx, modelID)

		responder, err := oc.resolveResponder(ctx, &PortalMetadata{
			ResolvedTarget: &ResolvedTarget{
				Kind:    ResolvedTargetModel,
				ModelID: modelID,
			},
		}, ResponderResolveOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to resolve model responder: %w", err)
		}

		var chatResp *bridgev2.CreateChatResponse
		if createChat {
			oc.loggerForContext(ctx).Info().Str("model", modelID).Msg("Creating new chat")
			chatResp, err = oc.createChat(ctx, chatCreateParams{ModelID: modelID, SkipRoomCreation: true})
			if err != nil {
				return nil, fmt.Errorf("failed to create chat: %w", err)
			}
		}

		resp := &bridgev2.ResolveIdentifierResponse{
			UserID:   userID,
			UserInfo: responderUserInfo(responder, modelContactIdentifiers(modelID), false),
			Ghost:    ghost,
			Chat:     chatResp,
		}
		if createChat && resp.Chat != nil && target.modelRedirect != "" {
			resp.Chat.DMRedirectedTo = target.modelRedirect
		}
		return resp, nil
	default:
		return nil, bridgev2.WrapRespErr(errors.New("identifier target is required"), mautrix.MInvalidParam)
	}
}

func (oc *AIClient) resolveChatGhost(ctx context.Context, userID networkid.UserID) (*bridgev2.Ghost, error) {
	if oc == nil || oc.UserLogin == nil || oc.UserLogin.Bridge == nil || userID == "" {
		return nil, nil
	}
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get ghost: %w", err)
	}
	return ghost, nil
}

// ResolveIdentifier resolves a model ID to a ghost and optionally creates a chat.
func (oc *AIClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	target, err := oc.resolveChatTargetFromIdentifier(ctx, identifier)
	if err != nil {
		return nil, err
	}
	return oc.resolveChatTargetResponse(ctx, target, createChat)
}

// CreateChatWithGhost creates a DM for a known model ghost.
func (oc *AIClient) CreateChatWithGhost(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.CreateChatResponse, error) {
	target, err := oc.resolveChatTargetFromGhost(ctx, ghost)
	if err != nil {
		return nil, err
	}
	resp, err := oc.resolveChatTargetResponse(ctx, target, true)
	if err != nil || resp == nil {
		return nil, err
	}
	return resp.Chat, nil
}
