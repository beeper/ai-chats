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

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/pkg/shared/stringutil"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

const openClawAgentCatalogTTL = 30 * time.Second

type openClawAgentProfile struct {
	AgentID   string
	Name      string
	AvatarURL string
	Emoji     string
}

// agentCatalogEntry bundles the cached agent list with metadata returned by the gateway.
type agentCatalogEntry struct {
	Agents    []gatewayAgentSummary
	DefaultID string
}

func cloneAgentCatalogEntry(e agentCatalogEntry) agentCatalogEntry {
	return agentCatalogEntry{
		Agents:    cloneGatewayAgentSummaries(e.Agents),
		DefaultID: e.DefaultID,
	}
}

func (oc *OpenClawClient) loadAgentCatalog(ctx context.Context, force bool) ([]gatewayAgentSummary, error) {
	if oc.agentCache == nil {
		return oc.mergeDiscoveredSessionAgents(nil), nil
	}
	entry, err := oc.agentCache.GetOrFetch(force, cloneAgentCatalogEntry, func() (agentCatalogEntry, error) {
		var gateway *gatewayWSClient
		if oc.manager != nil {
			gateway = oc.manager.gatewayClient()
		}
		if !oc.IsLoggedIn() || gateway == nil {
			return agentCatalogEntry{}, bridgev2.WrapRespErr(errors.New("you must be logged in to list contacts"), mautrix.MForbidden)
		}
		resp, err := gateway.ListAgents(ctx)
		if err != nil {
			return agentCatalogEntry{}, err
		}
		return agentCatalogEntry{
			Agents:    normalizeGatewayAgentSummaries(resp.Agents),
			DefaultID: strings.TrimSpace(resp.DefaultID),
		}, nil
	})
	if err != nil && len(entry.Agents) == 0 {
		return nil, err
	}
	return oc.mergeDiscoveredSessionAgents(entry.Agents), nil
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

func (oc *OpenClawClient) agentToResolveResponse(ctx context.Context, agent gatewayAgentSummary) (*bridgev2.ResolveIdentifierResponse, error) {
	agentID := strings.TrimSpace(agent.ID)
	ghost, err := oc.UserLogin.Bridge.GetGhostByID(ctx, openClawGhostUserID(agentID))
	if err != nil {
		return nil, fmt.Errorf("failed to get ghost for agent %s: %w", agentID, err)
	}
	return &bridgev2.ResolveIdentifierResponse{
		UserID:   openClawGhostUserID(agentID),
		UserInfo: oc.configuredAgentUserInfo(ctx, agent, ghost),
		Ghost:    ghost,
	}, nil
}

func (oc *OpenClawClient) agentsToResolveResponses(ctx context.Context, agents []gatewayAgentSummary) ([]*bridgev2.ResolveIdentifierResponse, error) {
	out := make([]*bridgev2.ResolveIdentifierResponse, 0, len(agents))
	for i := range agents {
		agentID := strings.TrimSpace(agents[i].ID)
		if agentID == "" || strings.EqualFold(agentID, "gateway") {
			continue
		}
		resp, err := oc.agentToResolveResponse(ctx, agents[i])
		if err != nil {
			return nil, err
		}
		out = append(out, resp)
	}
	return out, nil
}

func (oc *OpenClawClient) GetContactList(ctx context.Context) ([]*bridgev2.ResolveIdentifierResponse, error) {
	agents, err := oc.loadAgentCatalog(ctx, false)
	if err != nil {
		return nil, err
	}
	return oc.agentsToResolveResponses(ctx, sortConfiguredAgents(agents, oc.agentDefaultID(), ""))
}

func (oc *OpenClawClient) SearchUsers(ctx context.Context, query string) ([]*bridgev2.ResolveIdentifierResponse, error) {
	agents, err := oc.loadAgentCatalog(ctx, false)
	if err != nil {
		return nil, err
	}
	matches := sortConfiguredAgents(agents, oc.agentDefaultID(), query)
	out, err := oc.agentsToResolveResponses(ctx, matches)
	if err != nil {
		return nil, err
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
			agent, err := oc.agentSummaryOrVirtual(ctx, exactID)
			if err != nil {
				return nil, err
			}
			if agent != nil {
				resp, err := oc.agentToResolveResponse(ctx, *agent)
				if err != nil {
					return nil, err
				}
				out = append(out, resp)
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
	resp, err := oc.agentToResolveResponse(ctx, *agent)
	if err != nil {
		return nil, err
	}
	if createChat {
		chat, err := oc.createConfiguredAgentDM(ctx, *agent, resp.Ghost)
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
	meta.OpenClawDMTargetAgentName = stringutil.TrimDefault(oc.configuredAgentDisplayName(agent), meta.OpenClawDMTargetAgentName)
	meta.OpenClawDMCreatedFromContact = true
	meta.HistoryMode = "paginated"
	meta.RecentHistoryLimit = 0
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
	_, err = bridgesdk.EnsurePortalLifecycle(ctx, bridgesdk.PortalLifecycleOptions{
		Login:             oc.UserLogin,
		Portal:            portal,
		ChatInfo:          chatInfo,
		SaveBeforeCreate:  true,
		AIRoomKind:        agentremote.AIRoomKindAgent,
		ForceCapabilities: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to ensure openclaw dm portal room: %w", err)
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
	chatInfo := agentremote.BuildLoginDMChatInfo(agentremote.LoginDMChatInfoParams{
		Title:             displayName,
		Login:             oc.UserLogin,
		HumanUserIDPrefix: "openclaw-user",
		BotUserID:         openClawGhostUserID(agentID),
		BotDisplayName:    displayName,
		CanBackfill:       true,
	})
	if chatInfo == nil || chatInfo.Members == nil || chatInfo.Members.MemberMap == nil {
		return chatInfo
	}
	chatInfo.Topic = ptr.Ptr("OpenClaw agent DM")
	chatInfo.Members.MemberMap[humanUserID(oc.UserLogin.ID)] = bridgev2.ChatMember{
		EventSender: oc.senderForAgent(agentID, true),
		Membership:  event.MembershipJoin,
	}
	chatInfo.Members.MemberMap[openClawGhostUserID(agentID)] = bridgev2.ChatMember{
		EventSender: oc.senderForAgent(agentID, false),
		Membership:  event.MembershipJoin,
		UserInfo:    oc.sdkAgentForProfile(openClawAgentProfile{AgentID: agentID, Name: displayName}).UserInfo(),
		MemberEventExtra: map[string]any{
			"displayname": displayName,
		},
	}
	return chatInfo
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
	info := oc.sdkAgentForProfile(profile).UserInfo()
	desired := &GhostMetadata{
		OpenClawAgentID:        profile.AgentID,
		OpenClawAgentName:      profile.Name,
		OpenClawAgentAvatarURL: profile.AvatarURL,
		OpenClawAgentEmoji:     profile.Emoji,
		OpenClawAgentRole:      "assistant",
		LastSeenAt:             time.Now().UnixMilli(),
	}
	info.ExtraUpdates = func(_ context.Context, ghost *bridgev2.Ghost) bool {
		if ghost == nil {
			return false
		}
		current := ghostMeta(ghost)
		return applyGhostMetadataUpdates(current, desired)
	}
	if avatar := oc.agentAvatar(desired, profile.AgentID); avatar != nil {
		info.Avatar = avatar
	}
	return info
}

func (oc *OpenClawClient) displayNameFromAgentProfile(profile openClawAgentProfile) string {
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = oc.displayNameForAgent(profile.AgentID)
	}
	if emoji := strings.TrimSpace(profile.Emoji); emoji != "" && !strings.HasPrefix(name, emoji) {
		return emoji + " " + name
	}
	return name
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
		profile.AvatarURL = stringutil.TrimDefault(agent.Identity.Avatar, strings.TrimSpace(agent.Identity.AvatarURL))
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
			leftName := strings.ToLower(stringutil.TrimDefault(openClawAgentProfileFromSummary(&left).Name, leftID))
			rightName := strings.ToLower(stringutil.TrimDefault(openClawAgentProfileFromSummary(&right).Name, rightID))
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
		leftName := strings.ToLower(stringutil.TrimDefault(openClawAgentProfileFromSummary(&left).Name, leftID))
		rightName := strings.ToLower(stringutil.TrimDefault(openClawAgentProfileFromSummary(&right).Name, rightID))
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
	const noMatch = 10
	best := noMatch
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		switch {
		case candidate == query:
			return 0, true
		case strings.HasPrefix(candidate, query) && best > 1:
			best = 1
		case strings.Contains(candidate, query) && best > 2:
			best = 2
		}
	}
	if best == noMatch {
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

var (
	_ bridgev2.ContactListingNetworkAPI      = (*OpenClawClient)(nil)
	_ bridgev2.UserSearchingNetworkAPI       = (*OpenClawClient)(nil)
	_ bridgev2.IdentifierResolvingNetworkAPI = (*OpenClawClient)(nil)
	_ bridgev2.GhostDMCreatingNetworkAPI     = (*OpenClawClient)(nil)
)
