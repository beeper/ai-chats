package connector

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/beeper/ai-bridge/pkg/shared/stringutil"
	beeperdesktopapi "github.com/beeper/desktop-api-go"
)

type desktopAccountHint struct {
	AccountID    string
	Display      string
	InstanceKey  string
	BridgeType   string
	RawAccountID string
}

type desktopAccountHintsSnapshot struct {
	BaseURL string
	Items   []desktopAccountHint
}

func (oc *AIClient) buildDesktopAccountHintPrompt(ctx context.Context) string {
	snapshot := oc.collectDesktopAccountHints(ctx)
	return renderDesktopAccountHintPrompt(snapshot)
}

func (oc *AIClient) collectDesktopAccountHints(ctx context.Context) desktopAccountHintsSnapshot {
	if oc == nil {
		return desktopAccountHintsSnapshot{}
	}
	instanceNames := oc.desktopAPIInstanceNames()
	if len(instanceNames) == 0 {
		return desktopAccountHintsSnapshot{}
	}

	type instanceAccounts struct {
		instanceKey string
		baseURL     string
		accounts    map[string]desktopAccountHint
	}

	instances := make([]instanceAccounts, 0, len(instanceNames))
	baseURLs := make([]string, 0, len(instanceNames))

	for _, instance := range instanceNames {
		accountMap, err := oc.listDesktopAccounts(ctx, instance)
		if err != nil {
			oc.loggerForContext(ctx).Debug().Err(err).Str("instance", instance).Msg("Skipping desktop account hints for unreachable instance")
			continue
		}
		if len(accountMap) == 0 {
			continue
		}
		safeInstanceKey := sanitizeDesktopInstanceKey(instance)
		inst := instanceAccounts{
			instanceKey: safeInstanceKey,
			accounts:    make(map[string]desktopAccountHint, len(accountMap)),
		}
		if cfg, ok := oc.desktopAPIInstanceConfig(instance); ok {
			inst.baseURL = strings.TrimSpace(cfg.BaseURL)
			if inst.baseURL != "" {
				baseURLs = append(baseURLs, inst.baseURL)
			}
		}
		for _, account := range accountMap {
			rawAccountID := strings.TrimSpace(account.AccountID)
			if rawAccountID == "" {
				continue
			}
			bridgeType := normalizeDesktopBridgeType(account.Network)
			inst.accounts[rawAccountID] = desktopAccountHint{
				Display:      buildDesktopAccountDisplay(account),
				InstanceKey:  safeInstanceKey,
				BridgeType:   bridgeType,
				RawAccountID: rawAccountID,
			}
		}
		if len(inst.accounts) == 0 {
			continue
		}
		instances = append(instances, inst)
	}

	if len(instances) == 0 {
		return desktopAccountHintsSnapshot{}
	}
	slices.SortFunc(instances, func(a, b instanceAccounts) int {
		return cmp.Compare(a.instanceKey, b.instanceKey)
	})

	// Keep account ID prefixing consistent with sessions_list by using
	// configured instance count (not only currently reachable accounts).
	areThereMultipleDesktopInstances := len(instanceNames) > 1
	items := make([]desktopAccountHint, 0, 8)
	seenAccountIDs := map[string]struct{}{}
	for _, inst := range instances {
		rawIDs := make([]string, 0, len(inst.accounts))
		for rawID := range inst.accounts {
			rawIDs = append(rawIDs, rawID)
		}
		slices.Sort(rawIDs)
		for _, rawID := range rawIDs {
			item := inst.accounts[rawID]
			accountID := formatDesktopAccountID(areThereMultipleDesktopInstances, item.InstanceKey, item.BridgeType, item.RawAccountID)
			if accountID == "" {
				continue
			}
			if _, duplicate := seenAccountIDs[accountID]; duplicate {
				continue
			}
			seenAccountIDs[accountID] = struct{}{}
			item.AccountID = accountID
			items = append(items, item)
		}
	}

	return desktopAccountHintsSnapshot{
		BaseURL: chooseDesktopAccountHintBaseURL(baseURLs),
		Items:   items,
	}
}

func renderDesktopAccountHintPrompt(snapshot desktopAccountHintsSnapshot) string {
	if len(snapshot.Items) <= 1 {
		return ""
	}
	baseURL := strings.TrimSpace(snapshot.BaseURL)
	if baseURL == "" {
		baseURL = "baseURL unavailable"
	}
	lines := make([]string, 0, 1+len(snapshot.Items))
	lines = append(lines, fmt.Sprintf(`Accounts connected on Beeper Desktop API via connection "desktop" (%s)`, baseURL))
	for _, item := range snapshot.Items {
		if strings.TrimSpace(item.AccountID) == "" || strings.TrimSpace(item.Display) == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("$%s - %s", item.AccountID, item.Display))
	}
	if len(lines) <= 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func buildDesktopAccountDisplay(account beeperdesktopapi.Account) string {
	return buildDesktopAccountDisplayFromView(desktopAccountView{
		accountID:   account.AccountID,
		network:     account.Network,
		userID:      account.User.ID,
		fullName:    account.User.FullName,
		username:    account.User.Username,
		phoneNumber: account.User.PhoneNumber,
		email:       account.User.Email,
	})
}

func buildDesktopAccountDisplayFromView(account desktopAccountView) string {
	fullName := strings.TrimSpace(account.fullName)
	username := strings.TrimSpace(account.username)
	phone := strings.TrimSpace(account.phoneNumber)
	email := strings.TrimSpace(account.email)
	userID := strings.TrimSpace(account.userID)
	network := strings.TrimSpace(account.network)
	rawAccountID := strings.TrimSpace(account.accountID)

	base := stringutil.FirstNonEmpty(fullName, username, phone, email, userID, rawAccountID)
	if base == "" {
		base = "Unknown account"
	}

	seen := map[string]struct{}{
		strings.ToLower(base): {},
	}
	extra := make([]string, 0, 6)
	appendDisplayFragment(&extra, seen, "full name", fullName, base)
	appendDisplayFragment(&extra, seen, "username", username, base)
	appendDisplayFragment(&extra, seen, "phone", phone, base)
	appendDisplayFragment(&extra, seen, "email", email, base)
	appendDisplayFragment(&extra, seen, "user id", userID, base)
	appendDisplayFragment(&extra, seen, "account id", rawAccountID, base)
	appendDisplayFragment(&extra, seen, "network", network, base)

	if len(extra) == 0 {
		return base
	}
	return fmt.Sprintf("%s (%s)", base, strings.Join(extra, ", "))
}

type desktopAccountView struct {
	accountID   string
	network     string
	userID      string
	fullName    string
	username    string
	phoneNumber string
	email       string
}

func sanitizeDesktopInstanceKey(instance string) string {
	trimmed := strings.TrimSpace(strings.ToLower(instance))
	if trimmed == "" {
		return desktopDefaultInstance
	}
	var b strings.Builder
	wasUnderscore := false
	for _, r := range trimmed {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			b.WriteRune(r)
			wasUnderscore = false
			continue
		}
		if !wasUnderscore {
			b.WriteByte('_')
			wasUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return desktopDefaultInstance
	}
	return out
}

func normalizeDesktopBridgeType(network string) string {
	if out := canonicalDesktopNetwork(network); out != "" {
		return out
	}
	if out := normalizeDesktopNetworkToken(network); out != "" {
		return out
	}
	return "unknown"
}

func formatDesktopAccountID(areThereMultipleDesktopInstances bool, instanceKey, bridgeType, rawAccountID string) string {
	accountID := strings.TrimSpace(rawAccountID)
	if accountID == "" {
		return ""
	}
	bridge := normalizeDesktopBridgeType(bridgeType)
	if areThereMultipleDesktopInstances {
		instance := sanitizeDesktopInstanceKey(instanceKey)
		return fmt.Sprintf("%s_%s_%s", instance, bridge, accountID)
	}
	return fmt.Sprintf("%s_%s", bridge, accountID)
}

func chooseDesktopAccountHintBaseURL(baseURLs []string) string {
	unique := uniqueNonEmptyStrings(baseURLs)
	slices.Sort(unique)
	switch len(unique) {
	case 0:
		return ""
	case 1:
		return unique[0]
	default:
		return strings.Join(unique, ", ")
	}
}

func appendDisplayFragment(out *[]string, seen map[string]struct{}, label, value, base string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return
	}
	if strings.EqualFold(trimmed, base) {
		return
	}
	key := strings.ToLower(trimmed)
	if _, exists := seen[key]; exists {
		return
	}
	seen[key] = struct{}{}
	*out = append(*out, fmt.Sprintf("%s: %s", label, trimmed))
}
