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
	api *testMatrixAPI
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
func (tmc *testMatrixConnector) SendMessageStatus(context.Context, *bridgev2.MessageStatus, *bridgev2.MessageStatusEventInfo) {
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
	bridgeDB := database.New(networkid.BridgeID("bridge"), database.MetaTypes{
		Portal:    func() any { return &PortalMetadata{} },
		UserLogin: func() any { return &UserLoginMetadata{} },
		Ghost:     func() any { return &GhostMetadata{} },
		Message:   func() any { return &MessageMetadata{} },
	}, baseDB)
	if err = bridgeDB.Upgrade(context.Background()); err != nil {
		t.Fatalf("upgrade bridge db: %v", err)
	}

	childDB := aidb.NewChild(bridgeDB.Database, dbutil.NoopLogger)
	if err = aidb.EnsureSchema(context.Background(), childDB); err != nil {
		t.Fatalf("ensure ai schema: %v", err)
	}

	login := &database.UserLogin{
		ID:       networkid.UserLoginID("login"),
		Metadata: &UserLoginMetadata{Provider: provider},
	}
	userLogin := &bridgev2.UserLogin{
		UserLogin: login,
		Bridge:    &bridgev2.Bridge{DB: bridgeDB, Config: &bridgeconfig.BridgeConfig{}, Log: zerolog.Nop(), Matrix: &testMatrixConnector{}},
		Log:       zerolog.Nop(),
	}
	setUnexportedField(userLogin.Bridge, "ghostsByID", map[networkid.UserID]*bridgev2.Ghost{})
	setUnexportedField(userLogin.Bridge, "usersByMXID", map[id.UserID]*bridgev2.User{})
	setUnexportedField(userLogin.Bridge, "userLoginsByID", map[networkid.UserLoginID]*bridgev2.UserLogin{})
	setUnexportedField(userLogin.Bridge, "portalsByKey", map[networkid.PortalKey]*bridgev2.Portal{})
	setUnexportedField(userLogin.Bridge, "portalsByMXID", map[id.RoomID]*bridgev2.Portal{})
	return &AIClient{
		UserLogin: userLogin,
		connector: &OpenAIConnector{},
		log:       zerolog.Nop(),
	}
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
