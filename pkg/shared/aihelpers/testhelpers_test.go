package aihelpers

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/util/dbutil"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/event"
)

// baseTestClient provides a no-op implementation of bridgev2.NetworkAPI that
// test-specific client types can embed and selectively override.
type baseTestClient struct{}

func (baseTestClient) Connect(context.Context)                           {}
func (baseTestClient) Disconnect()                                       {}
func (baseTestClient) IsLoggedIn() bool                                  { return true }
func (baseTestClient) LogoutRemote(context.Context)                      {}
func (baseTestClient) IsThisUser(context.Context, networkid.UserID) bool { return false }
func (baseTestClient) GetChatInfo(context.Context, *bridgev2.Portal) (*bridgev2.ChatInfo, error) {
	return nil, nil
}
func (baseTestClient) GetUserInfo(context.Context, *bridgev2.Ghost) (*bridgev2.UserInfo, error) {
	return nil, nil
}
func (baseTestClient) GetCapabilities(context.Context, *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{}
}
func (baseTestClient) HandleMatrixMessage(context.Context, *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	return nil, nil
}

var _ bridgev2.NetworkAPI = baseTestClient{}

// newTestBridgeDB creates an in-memory SQLite bridge database for tests.
func newTestBridgeDB(t *testing.T) *database.Database {
	t.Helper()
	raw, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })

	db, err := dbutil.NewWithDB(raw, "sqlite3")
	if err != nil {
		t.Fatalf("wrap db: %v", err)
	}
	bridgeDB := database.New(networkid.BridgeID("bridge"), database.MetaTypes{}, db)
	if err = bridgeDB.Upgrade(context.Background()); err != nil {
		t.Fatalf("upgrade bridge db: %v", err)
	}
	return bridgeDB
}
