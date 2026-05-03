package codex

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/beeper/agentremote/pkg/shared/stringutil"
)

const codexThreadListPageSize = 100

var codexThreadListSourceKinds = []string{"cli", "vscode", "appServer"}

type codexThread struct {
	ID        string      `json:"id"`
	Preview   string      `json:"preview"`
	Name      string      `json:"name"`
	Path      string      `json:"path"`
	Cwd       string      `json:"cwd"`
	CreatedAt int64       `json:"createdAt"`
	UpdatedAt int64       `json:"updatedAt"`
	Turns     []codexTurn `json:"turns"`
}

type codexThreadListResponse struct {
	Data       []codexThread `json:"data"`
	NextCursor string        `json:"nextCursor"`
}

type codexThreadReadResponse struct {
	Thread codexThread `json:"thread"`
}

type codexTurn struct {
	ID     string          `json:"id"`
	Status string          `json:"status"`
	Items  []codexTurnItem `json:"items"`
}

type codexTurnItem struct {
	Type    string           `json:"type"`
	ID      string           `json:"id"`
	Text    string           `json:"text"`
	Content []codexUserInput `json:"content"`
}

type codexUserInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexBackfillEntry struct {
	MessageID   networkid.MessageID
	Sender      bridgev2.EventSender
	Text        string
	Role        string
	TurnID      string
	Timestamp   time.Time
	StreamOrder int64
}

type codexTurnTiming struct {
	TurnID             string
	UserTimestamp      time.Time
	AssistantTimestamp time.Time
	explicit           bool
}

type codexRolloutLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexRolloutEvent struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexRolloutTurnEvent struct {
	TurnID string `json:"turn_id"`
}

func (cc *CodexClient) syncStoredCodexThreads(ctx context.Context) error {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return nil
	}
	if err := cc.ensureRPC(ctx); err != nil {
		return err
	}
	directories := managedCodexPaths(loginMetadata(cc.UserLogin))
	if len(directories) == 0 {
		return nil
	}
	totalCreated := 0
	for _, directory := range directories {
		_, createdCount, err := cc.syncStoredCodexThreadsForPath(ctx, directory)
		if err != nil {
			return err
		}
		totalCreated += createdCount
	}
	if totalCreated > 0 {
		cc.log.Info().Int("created_rooms", totalCreated).Msg("Synced stored Codex threads into Matrix")
	}
	return nil
}

func (cc *CodexClient) syncStoredCodexThreadsForPath(ctx context.Context, cwd string) (int, int, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return 0, 0, nil
	}
	threads, err := cc.listCodexThreads(ctx, cwd)
	if err != nil {
		return 0, 0, err
	}
	if len(threads) == 0 {
		return 0, 0, nil
	}
	portalsByThreadID, err := cc.existingCodexPortalsByThreadID(ctx)
	if err != nil {
		return 0, 0, err
	}
	createdCount := 0
	for _, thread := range threads {
		threadID := strings.TrimSpace(thread.ID)
		if threadID == "" {
			continue
		}
		portal, created, err := cc.ensureCodexThreadPortal(ctx, portalsByThreadID[threadID], thread)
		if err != nil {
			cc.log.Warn().Err(err).Str("thread_id", threadID).Str("cwd", cwd).Msg("Failed to sync Codex thread portal")
			continue
		}
		portalsByThreadID[threadID] = portal
		if created {
			createdCount++
		}
	}
	return len(threads), createdCount, nil
}

func (cc *CodexClient) existingCodexPortalsByThreadID(ctx context.Context) (map[string]*bridgev2.Portal, error) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil || cc.UserLogin.Bridge.DB == nil {
		return map[string]*bridgev2.Portal{}, nil
	}
	records, err := listCodexPortalStateRecords(ctx, cc.UserLogin)
	if err != nil {
		return nil, err
	}
	out := make(map[string]*bridgev2.Portal, len(records))
	for _, record := range records {
		if record.State == nil || record.Portal == nil {
			continue
		}
		threadID := strings.TrimSpace(record.State.CodexThreadID)
		if threadID == "" {
			continue
		}
		if _, exists := out[threadID]; exists {
			continue
		}
		out[threadID] = record.Portal
	}
	return out, nil
}

func (cc *CodexClient) ensureCodexThreadPortal(ctx context.Context, existing *bridgev2.Portal, thread codexThread) (*bridgev2.Portal, bool, error) {
	if cc == nil || cc.UserLogin == nil || cc.UserLogin.Bridge == nil {
		return nil, false, errors.New("login unavailable")
	}
	threadID := strings.TrimSpace(thread.ID)
	if threadID == "" {
		return nil, false, errors.New("missing thread id")
	}

	portal := existing
	var err error
	if portal == nil {
		portalKey, keyErr := codexThreadPortalKey(cc.UserLogin.ID, threadID)
		if keyErr != nil {
			return nil, false, keyErr
		}
		portal, err = cc.UserLogin.Bridge.GetPortalByKey(ctx, portalKey)
		if err != nil {
			return nil, false, err
		}
	}
	var created bool
	if portal.Metadata == nil {
		portal.Metadata = &PortalMetadata{}
	}
	portalMeta(portal).IsCodexRoom = true
	state, err := loadCodexPortalState(ctx, portal)
	if err != nil {
		return nil, false, err
	}
	state.CodexThreadID = threadID
	state.ManagedImport = true
	if cwd := strings.TrimSpace(thread.Cwd); cwd != "" {
		state.CodexCwd = cwd
	}
	state.AwaitingCwdSetup = strings.TrimSpace(state.CodexCwd) == ""

	title := codexThreadTitle(thread)
	if title == "" {
		title = "Codex"
	}
	state.Title = title
	if state.Slug == "" {
		state.Slug = codexThreadSlug(threadID)
	}

	info := cc.composeCodexChatInfo(portal, state, true)
	portal, created, err = cc.bootstrapCodexPortal(ctx, portal, networkid.PortalKey{}, title, state, info, true)
	if err != nil {
		return nil, false, err
	}
	if created {
		if state.AwaitingCwdSetup {
			cc.sendSystemNotice(ctx, portal, "This imported conversation needs a working directory. Send an absolute path or `~/...`.")
		}
	} else {
		cc.UserLogin.Bridge.WakeupBackfillQueue()
	}
	if portal != nil && portal.MXID != "" {
		if info := cc.composeCodexChatInfo(portal, state, strings.TrimSpace(state.CodexThreadID) != ""); info != nil {
			portal.UpdateInfo(ctx, info, cc.UserLogin, nil, time.Time{})
		}
	}

	return portal, created, nil
}

func codexThreadTitle(thread codexThread) string {
	if title := strings.TrimSpace(thread.Name); title != "" {
		return title
	}
	preview := strings.TrimSpace(thread.Preview)
	if preview == "" {
		return ""
	}
	// Use only the first line, truncated to 120 characters.
	line, _, _ := strings.Cut(strings.ReplaceAll(preview, "\r", ""), "\n")
	const maxLen = 120
	if len(line) > maxLen {
		line = line[:maxLen]
	}
	return strings.TrimSpace(line)
}

func codexThreadSlug(threadID string) string {
	return "thread-" + stringutil.ShortHash(strings.TrimSpace(threadID), 6)
}
