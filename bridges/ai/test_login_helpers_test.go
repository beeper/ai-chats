package ai

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"unsafe"

	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/bridgev2/status"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"

	"github.com/beeper/agentremote/pkg/aidb"
)

type testMatrixConnector struct {
	api         *testMatrixAPI
	statuses    []bridgev2.MessageStatus
	statusInfos []*bridgev2.MessageStatusEventInfo
}

func (tmc *testMatrixConnector) Init(*bridgev2.Bridge)       {}
func (tmc *testMatrixConnector) Start(context.Context) error { return nil }
func (tmc *testMatrixConnector) PreStop()                    {}
func (tmc *testMatrixConnector) Stop()                       {}
func (tmc *testMatrixConnector) GetCapabilities() *bridgev2.MatrixCapabilities {
	return &bridgev2.MatrixCapabilities{}
}
func (tmc *testMatrixConnector) ParseGhostMXID(id.UserID) (networkid.UserID, bool) {
	return "", false
}
func (tmc *testMatrixConnector) GhostIntent(networkid.UserID) bridgev2.MatrixAPI {
	if tmc.api == nil {
		tmc.api = &testMatrixAPI{}
	}
	return tmc.api
}
func (tmc *testMatrixConnector) NewUserIntent(context.Context, id.UserID, string) (bridgev2.MatrixAPI, string, error) {
	return tmc.GhostIntent(""), "", nil
}
func (tmc *testMatrixConnector) BotIntent() bridgev2.MatrixAPI { return tmc.GhostIntent("") }
func (tmc *testMatrixConnector) SendBridgeStatus(context.Context, *status.BridgeState) error {
	return nil
}
func (tmc *testMatrixConnector) SendMessageStatus(_ context.Context, status *bridgev2.MessageStatus, info *bridgev2.MessageStatusEventInfo) {
	if status != nil {
		tmc.statuses = append(tmc.statuses, *status)
	}
	if info != nil {
		copied := *info
		tmc.statusInfos = append(tmc.statusInfos, &copied)
	}
}
func (tmc *testMatrixConnector) GenerateContentURI(context.Context, networkid.MediaID) (id.ContentURIString, error) {
	return "", nil
}
func (tmc *testMatrixConnector) GetPowerLevels(context.Context, id.RoomID) (*event.PowerLevelsEventContent, error) {
	return nil, nil
}
func (tmc *testMatrixConnector) GetMembers(context.Context, id.RoomID) (map[id.UserID]*event.MemberEventContent, error) {
	return nil, nil
}
func (tmc *testMatrixConnector) GetMemberInfo(context.Context, id.RoomID, id.UserID) (*event.MemberEventContent, error) {
	return nil, nil
}
func (tmc *testMatrixConnector) BatchSend(context.Context, id.RoomID, *mautrix.ReqBeeperBatchSend, []*bridgev2.MatrixSendExtra) (*mautrix.RespBeeperBatchSend, error) {
	return nil, nil
}
func (tmc *testMatrixConnector) GenerateDeterministicRoomID(networkid.PortalKey) id.RoomID {
	return ""
}
func (tmc *testMatrixConnector) GenerateDeterministicEventID(id.RoomID, networkid.PortalKey, networkid.MessageID, networkid.PartID) id.EventID {
	return ""
}
func (tmc *testMatrixConnector) GenerateReactionEventID(id.RoomID, *database.Message, networkid.UserID, networkid.EmojiID) id.EventID {
	return ""
}
func (tmc *testMatrixConnector) ServerName() string { return "example.com" }

func setUnexportedField(target any, field string, value any) {
	rv := reflect.ValueOf(target).Elem().FieldByName(field)
	reflect.NewAt(rv.Type(), unsafe.Pointer(rv.UnsafeAddr())).Elem().Set(reflect.ValueOf(value))
}

func newTestAIClientWithProvider(provider string) *AIClient {
	login := &database.UserLogin{
		BridgeID: networkid.BridgeID("bridge"),
		ID:       networkid.UserLoginID("login"),
		Metadata: &UserLoginMetadata{Provider: provider},
	}
	return &AIClient{
		UserLogin: &bridgev2.UserLogin{
			UserLogin: login,
			Log:       zerolog.Nop(),
		},
		connector: &OpenAIConnector{},
		log:       zerolog.Nop(),
	}
}

func newDBBackedTestAIClient(t *testing.T, provider string) *AIClient {
	t.Helper()

	raw, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })

	baseDB, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		t.Fatalf("wrap sqlite db: %v", err)
	}
	connector := NewAIConnector()
	bridge := bridgev2.NewBridge(
		networkid.BridgeID("bridge"),
		baseDB,
		zerolog.Nop(),
		&bridgeconfig.BridgeConfig{},
		&testMatrixConnector{},
		connector,
		func(*bridgev2.Bridge) bridgev2.CommandProcessor { return nil },
	)
	bridge.BackgroundCtx = context.Background()
	if err = bridge.DB.Upgrade(context.Background()); err != nil {
		t.Fatalf("upgrade bridge db: %v", err)
	}

	childDB := aidb.NewChild(bridge.DB.Database, dbutil.NoopLogger)
	if err = aidb.EnsureSchema(context.Background(), childDB); err != nil {
		t.Fatalf("ensure ai schema: %v", err)
	}

	user, err := bridge.GetUserByMXID(context.Background(), id.UserID("@alice:example.com"))
	if err != nil {
		t.Fatalf("get user by mxid: %v", err)
	}
	userLogin, err := user.NewLogin(context.Background(), &database.UserLogin{
		ID:         networkid.UserLoginID("login"),
		RemoteName: "AI",
		Metadata:   &UserLoginMetadata{Provider: provider},
	}, nil)
	if err != nil {
		t.Fatalf("new login: %v", err)
	}

	return &AIClient{
		UserLogin: userLogin,
		connector: connector,
		log:       zerolog.Nop(),
	}
}

func newDBBackedLoginHarness(t *testing.T) (*OpenAIConnector, *bridgev2.Bridge, *bridgev2.User) {
	t.Helper()

	raw, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })

	baseDB, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		t.Fatalf("wrap sqlite db: %v", err)
	}

	connector := NewAIConnector()
	bridge := bridgev2.NewBridge(
		networkid.BridgeID("bridge"),
		baseDB,
		zerolog.Nop(),
		&bridgeconfig.BridgeConfig{},
		&testMatrixConnector{},
		connector,
		func(*bridgev2.Bridge) bridgev2.CommandProcessor { return nil },
	)
	bridge.BackgroundCtx = context.Background()

	if err = bridge.DB.Upgrade(context.Background()); err != nil {
		t.Fatalf("upgrade bridge db: %v", err)
	}
	if err = aidb.EnsureSchema(context.Background(), aidb.NewChild(bridge.DB.Database, dbutil.NoopLogger)); err != nil {
		t.Fatalf("ensure ai schema: %v", err)
	}

	user, err := bridge.GetUserByMXID(context.Background(), id.UserID("@alice:example.com"))
	if err != nil {
		t.Fatalf("get user by mxid: %v", err)
	}
	return connector, bridge, user
}

func setTestLoginConfig(client *AIClient, cfg *aiLoginConfig) {
	if client == nil {
		return
	}
	client.loginConfig = cloneAILoginConfig(cfg)
}

func setTestLoginState(client *AIClient, state *loginRuntimeState) {
	if client == nil {
		return
	}
	client.loginState = cloneLoginRuntimeState(state)
}

func seedTestCustomAgent(t *testing.T, client *AIClient, agent *AgentDefinitionContent) {
	t.Helper()
	if err := saveCustomAgentForLogin(context.Background(), client.UserLogin, agent); err != nil {
		t.Fatalf("save custom agent: %v", err)
	}
}
