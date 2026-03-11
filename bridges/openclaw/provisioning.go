package openclaw

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"go.mau.fi/util/ptr"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote/pkg/bridgeadapter"
)

const openClawAgentCatalogTTL = 30 * time.Second

type openClawAgentProfile struct {
	AgentID   string
	Name      string
	AvatarURL string
	Emoji     string
}

func (oc *OpenClawClient) loadAgentCatalog(ctx context.Context, force bool) ([]gatewayAgentSummary, error) {
	now := time.Now()
	oc.agentCatalogMu.RLock()
	if !force && len(oc.agentCatalog) > 0 && now.Sub(oc.agentCatalogFetchedAt) < openClawAgentCatalogTTL {
		cached := oc.mergeDiscoveredSessionAgents(cloneGatewayAgentSummaries(oc.agentCatalog))
		oc.agentCatalogMu.RUnlock()
		return cached, nil
	}
	cached := oc.mergeDiscoveredSessionAgents(cloneGatewayAgentSummaries(oc.agentCatalog))
	oc.agentCatalogMu.RUnlock()

	var gateway *gatewayWSClient
	if oc.manager != nil {
		gateway = oc.manager.gatewayClient()
	}
	if !oc.IsLoggedIn() || gateway == nil {
		if len(cached) > 0 {
			return cached, nil
		}
		return nil, bridgev2.WrapRespErr(errors.New("you must be logged in to list contacts"), mautrix.MForbidden)
	}

	resp, err := gateway.ListAgents(ctx)
	if err != nil {
		if len(cached) > 0 {
			return cached, nil
		}
		return nil, err
	}
	agents := normalizeGatewayAgentSummaries(resp.Agents)
	agents = oc.mergeDiscoveredSessionAgents(agents)
	oc.agentCatalogMu.Lock()
	oc.agentCatalog = cloneGatewayAgentSummaries(agents)
	oc.agentCatalogDefaultID = strings.TrimSpace(resp.DefaultID)
	oc.agentCatalogMainKey = strings.TrimSpace(resp.MainKey)
	oc.agentCatalogScope = strings.TrimSpace(resp.Scope)
	oc.agentCatalogFetchedAt = now
	oc.agentCatalogMu.Unlock()
	return cloneGatewayAgentSummaries(agents), nil
}

func (oc *OpenClawClient) mergeDiscoveredSessionAgents(agents []gatewayAgentSummary) []gatewayAgentSummary {
	if oc == nil || oc.manager == nil {
		return agents
	}
	discovered := oc.manager.discoveredAgentIDs()
	if len(discovered) == 0 {
		return agents
	}
	merged := cloneGatewayAgentSummaries(agents)
	seen := make(map[string]struct{}, len(merged))
	for _, agent := range merged {
		agentID := strings.ToLower(strings.TrimSpace(agent.ID))
		if agentID != "" {
			seen[agentID] = struct{}{}
		}
	}
	for _, agentID := range discovered {
		key := strings.ToLower(strings.TrimSpace(agentID))
		if key == "" || strings.EqualFold(key, "gateway") {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, gatewayAgentSummary{ID: strings.TrimSpace(agentID)})
	}
	return merged
}

func (oc *OpenClawClient) agentCatalogEntryByID(ctx context.Context, agentID string) (*gatewayAgentSummary, error) {
	agents, err := oc.loadAgentCatalog(ctx, false)
	if err != nil {
		return nil, err
	}
	agentID = strings.TrimSpace(agentID)
	for i := range agents {
		if strings.EqualFold(strings.TrimSpace(agents[i].ID), agentID) {
			agent := agents[i]
			return &agent, nil
		}
	}
	return nil, nil
}

func openClawVirtualAgentSummary(agentID string) *gatewayAgentSummary {
	agentID = canonicalOpenClawAgentID(agentID)
	if agentID == "" || strings.EqualFold(agentID, "gateway") {
		return nil
	}
	return &gatewayAgentSummary{ID: agentID}
}

func (oc *OpenClawClient) agentSummaryOrVirtual(ctx context.Context, agentID string) (*gatewayAgentSummary, error) {
	agentID = canonicalOpenClawAgentID(agentID)
	if agentID == "" {
		return nil, nil
	}
	agent, err := oc.agentCatalogEntryByID(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent != nil {
		return agent, nil
	}
	return openClawVirtualAgentSummary(agentID), nil
}

func (oc *OpenClawClient) configuredAgentDisplayName(agent gatewayAgentSummary) string {
	profile := openClawAgentProfileFromSummary(&agent)
	return oc.displayNameFromAgentProfile(profile)
}

func (oc *OpenClawClient) configuredAgentIdentifiers(agentID string) []string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	return []string{"openclaw:" + agentID, agentID}
}

func (oc *OpenClawClient) configuredAgentUserInfo(ctx context.Context, agent gatewayAgentSummary, ghost *bridgev2.Ghost) *bridgev2.UserInfo {
	var existing *GhostMetadata
	if ghost != nil {
		existing = ghostMeta(ghost)
	}
	profile := oc.resolveAgentProfile(ctx, strings.TrimSpace(agent.ID), "", existing, &agent)
	return oc.userInfoForAgentProfile(profile)
}

func (oc *OpenClawClient) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	agents, err := oc.loadAgentCatalog(ctx, false)
	if err != nil {
		return nil, err
	}
	oc.agentCatalogMu.RLock()
	defaultID := strings.TrimSpace(oc.agentCatalogDefaultID)
	oc.agentCatalogMu.RUnlock()
	sorted := sortConfiguredAgents(agents, defaultID, "")
	out := make([]*bridgev2.ResolveIdentifierResponse, 0, len(sorted))
	for i := range sorted {
		agentID := strings.TrimSpace(sorted[i].ID)
		if agentID == "" || strings.EqualFold(agentID, "gateway") {
			continue
		}
		ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, openClawGhostUserID(agentID))
		if err != nil {
			return nil, fmt.Errorf("failed to get ghost for agent %s: %w", agentID, err)
		}
		out = append(out, &bridgev2.ResolveIdentifierResponse{
			UserID:   openClawGhostUserID(agentID),
			UserInfo: oc.configuredAgentUserInfo(ctx, sorted[i], ghost),
			Ghost:    ghost,
		})
	}
	return out, nil
}

func (oc *OpenClawClient) SearchUsers(ctx context.Context, query string) ([]*bridgev2.ResolveIdentifierResponse, error) {
	agents, err := oc.loadAgentCatalog(ctx, false)
	if err != nil {
		return nil, err
	}
	oc.agentCatalogMu.RLock()
	defaultID := strings.TrimSpace(oc.agentCatalogDefaultID)
	oc.agentCatalogMu.RUnlock()
	matches := sortConfiguredAgents(agents, defaultID, query)
	out := make([]*bridgev2.ResolveIdentifierResponse, 0, len(matches))
	for i := range matches {
		agentID := strings.TrimSpace(matches[i].ID)
		if agentID == "" || strings.EqualFold(agentID, "gateway") {
			continue
		}
		ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, openClawGhostUserID(agentID))
		if err != nil {
			return nil, fmt.Errorf("failed to get ghost for agent %s: %w", agentID, err)
		}
		out = append(out, &bridgev2.ResolveIdentifierResponse{
			UserID:   openClawGhostUserID(agentID),
			UserInfo: oc.configuredAgentUserInfo(ctx, matches[i], ghost),
			Ghost:    ghost,
		})
	}
	if exactID, ok := parseOpenClawResolvableIdentifier(query); ok {
		exactID = canonicalOpenClawAgentID(exactID)
		alreadyIncluded := false
		for _, match := range matches {
			if strings.EqualFold(strings.TrimSpace(match.ID), exactID) {
				alreadyIncluded = true
				break
			}
		}
		if !alreadyIncluded {
			if agent, err := oc.agentSummaryOrVirtual(ctx, exactID); err != nil {
				return nil, err
			} else if agent != nil {
				ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, openClawGhostUserID(agent.ID))
				if err != nil {
					return nil, fmt.Errorf("failed to get ghost for agent %s: %w", agent.ID, err)
				}
				out = append(out, &bridgev2.ResolveIdentifierResponse{
					UserID:   openClawGhostUserID(agent.ID),
					UserInfo: oc.configuredAgentUserInfo(ctx, *agent, ghost),
					Ghost:    ghost,
				})
			}
		}
	}
	return out, nil
}

func (oc *OpenClawClient) ResolveIdentifier(ctx context.Context, identifier string, createChat bool) (*bridgev2.ResolveIdentifierResponse, error) {
	agentID, ok := parseOpenClawResolvableIdentifier(identifier)
	if !ok {
		return nil, bridgev2.WrapRespErr(fmt.Errorf("identifier %q not found", identifier), mautrix.MNotFound)
	}
	agent, err := oc.agentSummaryOrVirtual(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, bridgev2.WrapRespErr(fmt.Errorf("identifier %q not found", identifier), mautrix.MNotFound)
	}
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, openClawGhostUserID(agent.ID))
	if err != nil {
		return nil, fmt.Errorf("failed to get ghost for agent %s: %w", agent.ID, err)
	}
	resp := &bridgev2.ResolveIdentifierResponse{
		UserID:   openClawGhostUserID(agent.ID),
		UserInfo: oc.configuredAgentUserInfo(ctx, *agent, ghost),
		Ghost:    ghost,
	}
	if createChat {
		chat, err := oc.createConfiguredAgentDM(ctx, *agent, ghost)
		if err != nil {
			return nil, err
		}
		resp.Chat = chat
	}
	return resp, nil
}

func (oc *OpenClawClient) CreateChatWithGhost(ctx context.Context, ghost *bridgev2.Ghost) (*bridgev2.CreateChatResponse, error) {
	if ghost == nil {
		return nil, bridgev2.WrapRespErr(errors.New("ghost is required"), mautrix.MInvalidParam)
	}
	agentID, ok := parseOpenClawGhostID(string(ghost.ID))
	if !ok {
		return nil, bridgev2.WrapRespErr(fmt.Errorf("unsupported ghost id %q", ghost.ID), mautrix.MInvalidParam)
	}
	agent, err := oc.agentSummaryOrVirtual(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if agent == nil {
		return nil, bridgev2.WrapRespErr(fmt.Errorf("agent %q not found", agentID), mautrix.MNotFound)
	}
	return oc.createConfiguredAgentDM(ctx, *agent, ghost)
}

func (oc *OpenClawClient) createConfiguredAgentDM(ctx context.Context, agent gatewayAgentSummary, ghost *bridgev2.Ghost) (*bridgev2.CreateChatResponse, error) {
	agentID := strings.TrimSpace(agent.ID)
	if agentID == "" {
		return nil, bridgev2.WrapRespErr(errors.New("agent id is required"), mautrix.MInvalidParam)
	}
	if ghost == nil {
		var err error
		ghost, err = oc.UserLogin.Bridge.GetGhostByID(ctx, openClawGhostUserID(agentID))
		if err != nil {
			return nil, fmt.Errorf("failed to get ghost for agent %s: %w", agentID, err)
		}
	}
	info := oc.configuredAgentUserInfo(ctx, agent, ghost)
	sessionKey := openClawDMAgentSessionKey(agentID)
	portal, err := oc.UserLogin.Bridge.GetPortalByKey(ctx, oc.portalKeyForSession(sessionKey))
	if err != nil {
		return nil, fmt.Errorf("failed to get portal for agent %s: %w", agentID, err)
	}
	meta := portalMeta(portal)
	meta.IsOpenClawRoom = true
	meta.OpenClawGatewayID = oc.gatewayID()
	meta.OpenClawSessionID = ""
	meta.OpenClawSessionKey = sessionKey
	meta.OpenClawAgentID = agentID
	meta.OpenClawDMTargetAgentID = agentID
	meta.OpenClawDMTargetAgentName = stringsTrimDefault(oc.configuredAgentDisplayName(agent), meta.OpenClawDMTargetAgentName)
	meta.OpenClawDMCreatedFromContact = true
	meta.HistoryMode = "recent_only"
	meta.RecentHistoryLimit = openClawDefaultSessionLimit
	portal.RoomType = database.RoomTypeDM
	portal.OtherUserID = openClawGhostUserID(agentID)
	portal.Name = meta.OpenClawDMTargetAgentName
	portal.Topic = "OpenClaw agent DM"
	portal.NameSet = true
	portal.TopicSet = true
	if err := portal.Save(ctx); err != nil {
		return nil, fmt.Errorf("failed to save openclaw dm portal: %w", err)
	}
	chatInfo := oc.syntheticDMPortalInfo(agentID, meta.OpenClawDMTargetAgentName)
	if chatInfo.Members != nil {
		member := chatInfo.Members.MemberMap[openClawGhostUserID(agentID)]
		member.UserInfo = info
		chatInfo.Members.MemberMap[openClawGhostUserID(agentID)] = member
	}
	if portal.MXID == "" {
		if err := portal.CreateMatrixRoom(ctx, oc.UserLogin, chatInfo); err != nil {
			return nil, fmt.Errorf("failed to create openclaw dm portal room: %w", err)
		}
		bridgeadapter.SendAIRoomInfo(ctx, portal, bridgeadapter.AIRoomKindAgent)
	}
	return &bridgev2.CreateChatResponse{
		PortalKey:  portal.PortalKey,
		Portal:     portal,
		PortalInfo: chatInfo,
	}, nil
}

func (oc *OpenClawClient) syntheticDMPortalInfo(agentID, displayName string) *bridgev2.ChatInfo {
	if strings.TrimSpace(displayName) == "" {
		displayName = oc.displayNameForAgent(agentID)
	}
	members := bridgev2.ChatMemberMap{
		humanUserID(oc.UserLogin.ID): {
			EventSender: bridgev2.EventSender{
				Sender:      humanUserID(oc.UserLogin.ID),
				SenderLogin: oc.UserLogin.ID,
				IsFromMe:    true,
			},
			Membership: event.MembershipJoin,
		},
		openClawGhostUserID(agentID): {
			EventSender: oc.senderForAgent(agentID, false),
			Membership:  event.MembershipJoin,
			UserInfo: &bridgev2.UserInfo{
				Name:        ptr.Ptr(displayName),
				IsBot:       ptr.Ptr(true),
				Identifiers: oc.configuredAgentIdentifiers(agentID),
			},
			MemberEventExtra: map[string]any{
				"displayname": displayName,
			},
		},
	}
	return &bridgev2.ChatInfo{
		Name:        ptr.Ptr(displayName),
		Topic:       ptr.Ptr("OpenClaw agent DM"),
		Type:        ptr.Ptr(database.RoomTypeDM),
		CanBackfill: true,
		Members: &bridgev2.ChatMemberList{
			IsFull:      true,
			OtherUserID: openClawGhostUserID(agentID),
			MemberMap:   members,
		},
	}
}

func (oc *OpenClawClient) resolveAgentProfile(ctx context.Context, agentID, sessionKey string, current *GhostMetadata, configured *gatewayAgentSummary) openClawAgentProfile {
	profile := openClawAgentProfileFromSummary(configured)
	fillStringIfEmpty(&profile.AgentID, strings.TrimSpace(agentID))
	if profile.AgentID != "" && !strings.EqualFold(profile.AgentID, "gateway") &&
		(profile.Name == "" || profile.AvatarURL == "" || profile.Emoji == "") {
		if identity := oc.lookupAgentIdentity(ctx, profile.AgentID, sessionKey); identity != nil {
			fillStringIfEmpty(&profile.AgentID, identity.AgentID)
			fillStringIfEmpty(&profile.Name, identity.Name)
			fillStringIfEmpty(&profile.AvatarURL, identity.Avatar, identity.AvatarURL)
			fillStringIfEmpty(&profile.Emoji, identity.Emoji)
		}
	}
	if current != nil {
		fillStringIfEmpty(&profile.AgentID, current.OpenClawAgentID)
		fillStringIfEmpty(&profile.Name, current.OpenClawAgentName)
		fillStringIfEmpty(&profile.AvatarURL, current.OpenClawAgentAvatarURL)
		fillStringIfEmpty(&profile.Emoji, current.OpenClawAgentEmoji)
	}
	fillStringIfEmpty(&profile.AgentID, strings.TrimSpace(agentID), "gateway")
	fillStringIfEmpty(&profile.Name, oc.displayNameForAgent(profile.AgentID))
	return profile
}

func (oc *OpenClawClient) userInfoForAgentProfile(profile openClawAgentProfile) *bridgev2.UserInfo {
	displayName := oc.displayNameFromAgentProfile(profile)
	meta := &GhostMetadata{
		OpenClawAgentID:        profile.AgentID,
		OpenClawAgentName:      profile.Name,
		OpenClawAgentAvatarURL: profile.AvatarURL,
		OpenClawAgentEmoji:     profile.Emoji,
		OpenClawAgentRole:      "assistant",
		LastSeenAt:             time.Now().UnixMilli(),
	}
	info := &bridgev2.UserInfo{
		Name:        ptr.Ptr(displayName),
		IsBot:       ptr.Ptr(true),
		Identifiers: oc.configuredAgentIdentifiers(profile.AgentID),
		ExtraUpdates: func(_ context.Context, ghost *bridgev2.Ghost) bool {
			if ghost == nil {
				return false
			}
			current := ghostMeta(ghost)
			changed := false
			if value := strings.TrimSpace(meta.OpenClawAgentID); value != "" && current.OpenClawAgentID != value {
				current.OpenClawAgentID = value
				changed = true
			}
			if value := strings.TrimSpace(meta.OpenClawAgentName); value != "" && current.OpenClawAgentName != value {
				current.OpenClawAgentName = value
				changed = true
			}
			if value := strings.TrimSpace(meta.OpenClawAgentAvatarURL); value != "" && current.OpenClawAgentAvatarURL != value {
				current.OpenClawAgentAvatarURL = value
				changed = true
			}
			if value := strings.TrimSpace(meta.OpenClawAgentEmoji); value != "" && current.OpenClawAgentEmoji != value {
				current.OpenClawAgentEmoji = value
				changed = true
			}
			if current.OpenClawAgentRole != "assistant" {
				current.OpenClawAgentRole = "assistant"
				changed = true
			}
			if current.LastSeenAt != meta.LastSeenAt {
				current.LastSeenAt = meta.LastSeenAt
				changed = true
			}
			return changed
		},
	}
	if avatar := oc.agentAvatar(meta, profile.AgentID); avatar != nil {
		info.Avatar = avatar
	}
	return info
}

func (oc *OpenClawClient) displayNameFromAgentProfile(profile openClawAgentProfile) string {
	meta := &GhostMetadata{
		OpenClawAgentID:    profile.AgentID,
		OpenClawAgentName:  profile.Name,
		OpenClawAgentEmoji: profile.Emoji,
	}
	return oc.formatAgentDisplayName(meta, profile.AgentID)
}

func openClawAgentProfileFromSummary(agent *gatewayAgentSummary) openClawAgentProfile {
	if agent == nil {
		return openClawAgentProfile{}
	}
	profile := openClawAgentProfile{
		AgentID: strings.TrimSpace(agent.ID),
	}
	if agent.Identity != nil {
		profile.Name = strings.TrimSpace(agent.Identity.Name)
		profile.AvatarURL = stringsTrimDefault(agent.Identity.Avatar, strings.TrimSpace(agent.Identity.AvatarURL))
		profile.Emoji = strings.TrimSpace(agent.Identity.Emoji)
	}
	fillStringIfEmpty(&profile.Name, strings.TrimSpace(agent.Name))
	return profile
}

func normalizeGatewayAgentSummaries(agents []gatewayAgentSummary) []gatewayAgentSummary {
	normalized := make([]gatewayAgentSummary, 0, len(agents))
	seen := make(map[string]struct{}, len(agents))
	for _, agent := range agents {
		agent.ID = strings.TrimSpace(agent.ID)
		agent.Name = strings.TrimSpace(agent.Name)
		agent.Identity = normalizeGatewayAgentIdentity(agent.Identity)
		if agent.ID == "" {
			continue
		}
		key := strings.ToLower(agent.ID)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, agent)
	}
	return normalized
}

func cloneGatewayAgentSummaries(agents []gatewayAgentSummary) []gatewayAgentSummary {
	cloned := make([]gatewayAgentSummary, len(agents))
	for i := range agents {
		cloned[i] = agents[i]
		if agents[i].Identity != nil {
			identity := *agents[i].Identity
			cloned[i].Identity = &identity
		}
	}
	return cloned
}

func parseOpenClawResolvableIdentifier(identifier string) (string, bool) {
	identifier = strings.TrimSpace(identifier)
	if identifier == "" {
		return "", false
	}
	if agentID, ok := parseOpenClawGhostID(identifier); ok {
		return agentID, true
	}
	if value, ok := strings.CutPrefix(identifier, "openclaw:"); ok {
		value = strings.TrimSpace(value)
		return value, value != ""
	}
	return identifier, true
}

func sortConfiguredAgents(agents []gatewayAgentSummary, defaultID, query string) []gatewayAgentSummary {
	query = strings.TrimSpace(strings.ToLower(query))
	filtered := make([]gatewayAgentSummary, 0, len(agents))
	for _, agent := range agents {
		agentID := strings.TrimSpace(agent.ID)
		if agentID == "" || strings.EqualFold(agentID, "gateway") {
			continue
		}
		if query != "" {
			if _, ok := configuredAgentMatchScore(agent, query); !ok {
				continue
			}
		}
		filtered = append(filtered, agent)
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		left, right := filtered[i], filtered[j]
		leftID := strings.TrimSpace(left.ID)
		rightID := strings.TrimSpace(right.ID)
		if query == "" {
			if strings.EqualFold(leftID, defaultID) != strings.EqualFold(rightID, defaultID) {
				return strings.EqualFold(leftID, defaultID)
			}
			leftName := strings.ToLower(stringsTrimDefault(openClawAgentProfileFromSummary(&left).Name, leftID))
			rightName := strings.ToLower(stringsTrimDefault(openClawAgentProfileFromSummary(&right).Name, rightID))
			if leftName != rightName {
				return leftName < rightName
			}
			return strings.ToLower(leftID) < strings.ToLower(rightID)
		}
		leftScore, _ := configuredAgentMatchScore(left, query)
		rightScore, _ := configuredAgentMatchScore(right, query)
		if leftScore != rightScore {
			return leftScore < rightScore
		}
		leftName := strings.ToLower(stringsTrimDefault(openClawAgentProfileFromSummary(&left).Name, leftID))
		rightName := strings.ToLower(stringsTrimDefault(openClawAgentProfileFromSummary(&right).Name, rightID))
		if leftName != rightName {
			return leftName < rightName
		}
		return strings.ToLower(leftID) < strings.ToLower(rightID)
	})
	return filtered
}

func configuredAgentMatchScore(agent gatewayAgentSummary, query string) (int, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0, true
	}
	candidates := []string{
		strings.ToLower(strings.TrimSpace(agent.ID)),
		strings.ToLower(strings.TrimSpace(agent.Name)),
	}
	if agent.Identity != nil {
		candidates = append(candidates, strings.ToLower(strings.TrimSpace(agent.Identity.Name)))
	}
	best := 10
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		switch {
		case candidate == query:
			if 0 < best {
				best = 0
			}
		case strings.HasPrefix(candidate, query):
			if 1 < best {
				best = 1
			}
		case strings.Contains(candidate, query):
			if 2 < best {
				best = 2
			}
		}
	}
	if best == 10 {
		return 0, false
	}
	return best, true
}

func fillStringIfEmpty(dst *string, values ...string) {
	if dst == nil || strings.TrimSpace(*dst) != "" {
		return
	}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			*dst = strings.TrimSpace(value)
			return
		}
	}
}

var _ bridgev2.ContactListingNetworkAPI = (*OpenClawClient)(nil)
var _ bridgev2.UserSearchingNetworkAPI = (*OpenClawClient)(nil)
var _ bridgev2.IdentifierResolvingNetworkAPI = (*OpenClawClient)(nil)
var _ bridgev2.GhostDMCreatingNetworkAPI = (*OpenClawClient)(nil)
