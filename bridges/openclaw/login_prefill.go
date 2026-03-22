package openclaw

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
)

const openClawPrefillFlowPrefix = "openclaw_prefill:"

func (oc *OpenClawConnector) loginPrefillTTL() time.Duration {
	if oc == nil {
		return 5 * time.Minute
	}
	seconds := oc.Config.OpenClaw.Discovery.PrefillTTLSeconds
	if seconds <= 0 {
		seconds = 300
	}
	return time.Duration(seconds) * time.Second
}

func (oc *OpenClawConnector) registerLoginPrefill(user *bridgev2.User, url, label string) (string, time.Time) {
	if oc == nil || user == nil {
		return "", time.Time{}
	}
	now := time.Now()
	expiresAt := now.Add(oc.loginPrefillTTL())
	entry := openClawLoginPrefill{
		UserMXID:  user.MXID,
		URL:       strings.TrimSpace(url),
		Label:     strings.TrimSpace(label),
		ExpiresAt: expiresAt,
	}
	id := openClawPrefillFlowPrefix + uuid.NewString()
	oc.prefillsMu.Lock()
	oc.pruneLoginPrefillsLocked(now)
	if oc.prefills == nil {
		oc.prefills = make(map[string]openClawLoginPrefill)
	}
	oc.prefills[id] = entry
	oc.prefillsMu.Unlock()
	return id, expiresAt
}

func (oc *OpenClawConnector) loginPrefill(flowID string, user *bridgev2.User) (openClawLoginPrefill, bool) {
	if oc == nil || user == nil || !strings.HasPrefix(flowID, openClawPrefillFlowPrefix) {
		return openClawLoginPrefill{}, false
	}
	now := time.Now()
	oc.prefillsMu.Lock()
	defer oc.prefillsMu.Unlock()
	oc.pruneLoginPrefillsLocked(now)
	prefill, ok := oc.prefills[flowID]
	if !ok || prefill.UserMXID != user.MXID {
		return openClawLoginPrefill{}, false
	}
	return prefill, true
}

func (oc *OpenClawConnector) pruneLoginPrefillsLocked(now time.Time) {
	if oc == nil || len(oc.prefills) == 0 {
		return
	}
	for id, prefill := range oc.prefills {
		if !prefill.ExpiresAt.IsZero() && !prefill.ExpiresAt.After(now) {
			delete(oc.prefills, id)
		}
	}
}
