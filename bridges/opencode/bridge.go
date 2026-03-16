package opencode

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/simplevent"
	"maunium.net/go/mautrix/event"

	"github.com/beeper/agentremote"
	"github.com/beeper/agentremote/bridges/opencode/api"
	"github.com/beeper/agentremote/pkg/shared/backfillutil"
	bridgesdk "github.com/beeper/agentremote/sdk"
)

// Host provides the minimal surface area the OpenCode bridge needs
// to integrate with the surrounding connector.
type Host interface {
	Log() *zerolog.Logger
	GetUserLogin() *bridgev2.UserLogin
	BackgroundContext(ctx context.Context) context.Context
	SendSystemNotice(ctx context.Context, portal *bridgev2.Portal, msg string)
	EmitOpenCodeStreamEvent(ctx context.Context, portal *bridgev2.Portal, turnID, agentID string, part map[string]any)
	FinishOpenCodeStream(turnID string)
	DownloadAndEncodeMedia(ctx context.Context, mediaURL string, file *event.EncryptedFileInfo, maxMB int) (string, string, error)
	SetRoomName(ctx context.Context, portal *bridgev2.Portal, name string) error
	SenderForOpenCode(instanceID string, fromMe bool) bridgev2.EventSender
	CleanupPortal(ctx context.Context, portal *bridgev2.Portal, reason string)
	PortalMeta(portal *bridgev2.Portal) *PortalMeta
	SetPortalMeta(portal *bridgev2.Portal, meta *PortalMeta)
	SavePortal(ctx context.Context, portal *bridgev2.Portal) error
	DefaultAgentID() string
	OpenCodeInstances() map[string]*OpenCodeInstance
	SaveOpenCodeInstances(ctx context.Context, instances map[string]*OpenCodeInstance) error
	HumanUserID(loginID networkid.UserLoginID) networkid.UserID
	ensureStreamWriter(ctx context.Context, portal *bridgev2.Portal, turnID, agentID string) (*openCodeStreamState, *bridgesdk.Writer)
	applyStreamMessageMetadata(state *openCodeStreamState, metadata map[string]any)
}

// PortalMeta is the OpenCode-specific view of portal metadata.
type PortalMeta struct {
	IsOpenCodeRoom bool
	InstanceID     string
	SessionID      string
	ReadOnly       bool
	TitlePending   bool
	Title          string
	TitleGenerated bool
	AgentID        string
	VerboseLevel   string
	AwaitingPath   bool
}

// OpenCodeInstance stores connection details for an OpenCode server.
type OpenCodeInstance struct {
	ID               string `json:"id,omitempty"`
	Mode             string `json:"mode,omitempty"`
	URL              string `json:"url,omitempty"`
	Username         string `json:"username,omitempty"`
	Password         string `json:"password,omitempty"`
	HasPassword      bool   `json:"has_password,omitempty"`
	BinaryPath       string `json:"binary_path,omitempty"`
	DefaultDirectory string `json:"default_directory,omitempty"`
	WorkingDirectory string `json:"working_directory,omitempty"`
	LauncherID       string `json:"launcher_id,omitempty"`
}

// Bridge coordinates OpenCode sessions with Matrix rooms.
type Bridge struct {
	host          Host
	manager       *OpenCodeManager
	orderingMu    sync.Mutex
	liveOrderByID map[string]int64
}

func NewBridge(host Host) *Bridge {
	if host == nil {
		return nil
	}
	bridge := &Bridge{host: host, liveOrderByID: make(map[string]int64)}
	if log := host.Log(); log != nil {
		log.Info().Msg("Initializing OpenCode bridge")
	}
	bridge.manager = NewOpenCodeManager(bridge)
	return bridge
}

func (b *Bridge) AbortSession(ctx context.Context, instanceID, sessionID string) error {
	if b == nil || b.manager == nil {
		return ErrUnavailable
	}
	return b.manager.AbortSession(ctx, instanceID, sessionID)
}

// ApprovalHandler returns the manager's ApprovalFlow as an ApprovalReactionHandler, or nil if unavailable.
func (b *Bridge) ApprovalHandler() agentremote.ApprovalReactionHandler {
	if b == nil || b.manager == nil {
		return nil
	}
	return b.manager.approvalFlow
}

func (b *Bridge) RestoreConnections(ctx context.Context) error {
	if b == nil || b.manager == nil {
		return nil
	}
	return b.manager.RestoreConnections(ctx)
}

func (b *Bridge) DisconnectAll() {
	if b == nil || b.manager == nil {
		return
	}
	b.manager.DisconnectAll()
}

var (
	ErrUnavailable = bridgeError("OpenCode integration is not available")
)

type bridgeError string

func (e bridgeError) Error() string { return string(e) }

// ---------- bridge internal helpers ----------

func (b *Bridge) queueRemoteEvent(ev bridgev2.RemoteEvent) {
	if b == nil || b.host == nil || ev == nil {
		return
	}
	login := b.host.GetUserLogin()
	if login == nil {
		return
	}
	login.QueueRemoteEvent(ev)
}

func (b *Bridge) nextLiveStreamOrder(instanceID, sessionID string, ts time.Time) int64 {
	if b == nil {
		return backfillutil.NextStreamOrder(0, ts)
	}
	key := instanceID + ":" + sessionID
	if key == ":" {
		key = instanceID
	}
	b.orderingMu.Lock()
	defer b.orderingMu.Unlock()
	next := backfillutil.NextStreamOrder(b.liveOrderByID[key], ts)
	b.liveOrderByID[key] = next
	return next
}

func (b *Bridge) emitOpenCodeStreamEvent(ctx context.Context, portal *bridgev2.Portal, turnID, agentID string, part map[string]any) {
	if b == nil || b.host == nil {
		return
	}
	b.host.EmitOpenCodeStreamEvent(ctx, portal, turnID, agentID, part)
}

func (b *Bridge) finishOpenCodeStream(turnID string) {
	if b == nil || b.host == nil {
		return
	}
	b.host.FinishOpenCodeStream(turnID)
}

func (b *Bridge) portalMeta(portal *bridgev2.Portal) *PortalMeta {
	if b == nil || b.host == nil || portal == nil {
		return nil
	}
	meta := b.host.PortalMeta(portal)
	if meta == nil {
		meta = &PortalMeta{}
	}
	return meta
}

func (b *Bridge) portalAgentID(portal *bridgev2.Portal) string {
	if meta := b.portalMeta(portal); meta != nil {
		return meta.AgentID
	}
	return ""
}

func openCodeSessionTimestamp(session api.Session) time.Time {
	if session.Time.Updated > 0 {
		return time.UnixMilli(int64(session.Time.Updated))
	}
	if session.Time.Created > 0 {
		return time.UnixMilli(int64(session.Time.Created))
	}
	return time.Time{}
}

func buildOpenCodeSessionResync(loginID networkid.UserLoginID, instanceID string, session api.Session) *simplevent.ChatResync {
	return &simplevent.ChatResync{
		EventMeta: simplevent.EventMeta{
			Type:      bridgev2.RemoteEventChatResync,
			PortalKey: OpenCodePortalKey(loginID, instanceID, session.ID),
			Timestamp: openCodeSessionTimestamp(session),
		},
		LatestMessageTS: openCodeSessionTimestamp(session),
	}
}

func (b *Bridge) queueOpenCodeSessionResync(instanceID string, session api.Session) {
	if b == nil || b.host == nil || strings.TrimSpace(session.ID) == "" {
		return
	}
	login := b.host.GetUserLogin()
	if login == nil {
		return
	}
	b.queueRemoteEvent(buildOpenCodeSessionResync(login.ID, instanceID, session))
}

func (b *Bridge) listAllChatPortals(ctx context.Context) ([]*bridgev2.Portal, error) {
	if b == nil || b.host == nil {
		return nil, nil
	}
	login := b.host.GetUserLogin()
	if login == nil || login.Bridge == nil || login.Bridge.DB == nil {
		return nil, nil
	}
	allDBPortals, err := login.Bridge.DB.Portal.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	var portals []*bridgev2.Portal
	for _, dbPortal := range allDBPortals {
		if dbPortal.Receiver != login.ID {
			continue
		}
		portal, err := login.Bridge.GetPortalByKey(ctx, dbPortal.PortalKey)
		if err != nil {
			return nil, err
		}
		if portal != nil {
			portals = append(portals, portal)
		}
	}
	return portals, nil
}
