package opencodebridge

import (
	"context"

	"github.com/rs/zerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

// Host provides the minimal surface area the OpenCode bridge needs
// to integrate with the surrounding connector.
type Host interface {
	Log() *zerolog.Logger
	Login() *bridgev2.UserLogin
	BackgroundContext(ctx context.Context) context.Context
	SendSystemNotice(ctx context.Context, portal *bridgev2.Portal, msg string)
	SendPendingStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event, msg string)
	SendSuccessStatus(ctx context.Context, portal *bridgev2.Portal, evt *event.Event)
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
	RoomCapabilitiesEventType() event.Type
	RoomSettingsEventType() event.Type
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
	DefaultAgentID string
}

// OpenCodeInstance stores connection details for an OpenCode server.
type OpenCodeInstance struct {
	ID       string `json:"id,omitempty"`
	URL      string `json:"url,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// Bridge coordinates OpenCode sessions with Matrix rooms.
type Bridge struct {
	host    Host
	manager *OpenCodeManager
}

func NewBridge(host Host) *Bridge {
	if host == nil {
		return nil
	}
	bridge := &Bridge{host: host}
	bridge.manager = NewOpenCodeManager(bridge)
	return bridge
}

func (b *Bridge) Host() Host {
	if b == nil {
		return nil
	}
	return b.host
}

func (b *Bridge) Manager() *OpenCodeManager {
	if b == nil {
		return nil
	}
	return b.manager
}

func (b *Bridge) DisplayName(instanceID string) string {
	if b == nil {
		return ""
	}
	return b.opencodeDisplayName(instanceID)
}

func (b *Bridge) InstanceConfig(instanceID string) *OpenCodeInstance {
	if b == nil {
		return nil
	}
	return b.opencodeInstanceConfig(instanceID)
}

func (b *Bridge) EnsureGhostDisplayName(ctx context.Context, instanceID string) {
	if b == nil {
		return
	}
	b.ensureOpenCodeGhostDisplayName(ctx, instanceID)
}

func (b *Bridge) CreateSessionChat(ctx context.Context, instanceID, title string, pendingTitle bool) (*bridgev2.CreateChatResponse, error) {
	if b == nil {
		return nil, ErrUnavailable
	}
	return b.createOpenCodeSessionChat(ctx, instanceID, title, pendingTitle)
}

func (b *Bridge) IsAvailable() bool {
	return b != nil && b.manager != nil
}

func (b *Bridge) IsConnected(instanceID string) bool {
	if b == nil || b.manager == nil {
		return false
	}
	return b.manager.IsConnected(instanceID)
}

func (b *Bridge) RestoreConnections(ctx context.Context) error {
	if b == nil || b.manager == nil {
		return nil
	}
	return b.manager.RestoreConnections(ctx)
}

func (b *Bridge) Connect(ctx context.Context, baseURL, password, username string) (*OpenCodeInstance, int, error) {
	if b == nil || b.manager == nil {
		return nil, 0, ErrUnavailable
	}
	inst, count, err := b.manager.Connect(ctx, baseURL, password, username)
	if inst == nil || err != nil {
		return nil, count, err
	}
	return &inst.cfg, count, err
}

var (
	ErrUnavailable      = bridgeError("OpenCode integration is not available")
	ErrInstanceNotFound = bridgeError("OpenCode instance not found")
)

type bridgeError string

func (e bridgeError) Error() string { return string(e) }
