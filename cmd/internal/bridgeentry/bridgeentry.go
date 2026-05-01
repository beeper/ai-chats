package bridgeentry

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"go.mau.fi/util/dbutil"
	"go.mau.fi/util/exzerolog"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/bridgeconfig"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/matrix"
	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"
	"maunium.net/go/mautrix/bridgev2/networkid"
)

const (
	RepoURL = "https://github.com/beeper/agentremote"
	Version = "0.1.0"
)

type Definition struct {
	Name        string
	Description string
	Port        int
	DBName      string
}

var (
	AI = Definition{
		Name:        "ai",
		Description: "AI bridge built with the AgentRemote SDK.",
		Port:        29345,
		DBName:      "ai.db",
	}
	Codex = Definition{
		Name:        "codex",
		Description: "Codex bridge built with the AgentRemote SDK.",
		Port:        29346,
		DBName:      "codex.db",
	}
	DummyBridge = Definition{
		Name:        "dummybridge",
		Description: "DummyBridge demo bridge built with the AgentRemote SDK.",
		Port:        29349,
		DBName:      "dummybridge.db",
	}
)

func (d Definition) NewMain(connector bridgev2.NetworkConnector) *mxmain.BridgeMain {
	return &mxmain.BridgeMain{
		Name:        d.Name,
		Description: d.Description,
		URL:         RepoURL,
		Version:     Version,
		Connector:   connector,
	}
}

func Run(def Definition, connector bridgev2.NetworkConnector, tag, commit, buildTime string) {
	m := def.NewMain(connector)
	RunMain(def, m, tag, commit, buildTime)
}

func RunMain(def Definition, m *mxmain.BridgeMain, tag, commit, buildTime string) {
	if m == nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to initialize bridge: missing main")
		os.Exit(12)
	}
	m.InitVersion(tag, commit, buildTime)
	m.PreInit()
	initWithCanonicalBridgeID(def, m)
	m.Start()
	exitCode := m.WaitForInterrupt()
	m.Stop()
	os.Exit(exitCode)
}

func initWithCanonicalBridgeID(def Definition, m *mxmain.BridgeMain) {
	var err error
	m.Log, err = m.Config.Logging.Compile()
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "Failed to initialize logger:", err)
		os.Exit(12)
	}
	exzerolog.SetupDefaults(m.Log)
	err = validateConfig(m)
	if err != nil {
		m.Log.WithLevel(zerolog.FatalLevel).Err(err).Msg("Configuration error")
		m.Log.Info().Msg("See https://docs.mau.fi/faq/field-unconfigured for more info")
		os.Exit(11)
	}

	m.Log.Info().
		Str("name", m.Name).
		Str("version", m.Version).
		Str("go_version", runtime.Version()).
		Msg("Initializing bridge")

	initDB(m)

	bridgeID := resolveBridgeID(def, m.Connector)
	if bridgeID == "" {
		m.Log.Fatal().Msg("Failed to resolve canonical bridge ID")
	}

	m.Matrix = matrix.NewConnector(m.Config)
	m.Matrix.OnWebsocketReplaced = func() {
		m.TriggerStop(0)
	}
	m.Bridge = bridgev2.NewBridge(bridgeID, m.DB, *m.Log, &m.Config.Bridge, m.Matrix, m.Connector, commands.NewProcessor)
	m.Matrix.AS.DoublePuppetValue = m.Name
	m.Bridge.Commands.(*commands.Processor).AddHandler(&commands.FullHandler{
		Func: func(ce *commands.Event) {
			ce.Reply(m.Version)
		},
		Name: "version",
		Help: commands.HelpMeta{
			Section:     commands.HelpSectionGeneral,
			Description: "Get the bridge version.",
		},
	})
	if m.PostInit != nil {
		m.PostInit()
	}
}

func resolveBridgeID(def Definition, connector bridgev2.NetworkConnector) networkid.BridgeID {
	if connector != nil {
		if id := strings.TrimSpace(connector.GetName().NetworkID); id != "" {
			return networkid.BridgeID(id)
		}
	}
	return networkid.BridgeID(strings.TrimSpace(def.Name))
}

func initDB(m *mxmain.BridgeMain) {
	m.Log.Debug().Msg("Initializing database connection")
	dbConfig := m.Config.Database
	if dbConfig.Type == "sqlite3" {
		m.Log.WithLevel(zerolog.FatalLevel).Msg("Invalid database type sqlite3. Use sqlite3-fk-wal instead.")
		os.Exit(14)
	}
	if (dbConfig.Type == "sqlite3-fk-wal" || dbConfig.Type == "litestream") && dbConfig.MaxOpenConns != 1 && !strings.Contains(dbConfig.URI, "_txlock=immediate") {
		var fixedExampleURI string
		if !strings.HasPrefix(dbConfig.URI, "file:") {
			fixedExampleURI = fmt.Sprintf("file:%s?_txlock=immediate", dbConfig.URI)
		} else if !strings.ContainsRune(dbConfig.URI, '?') {
			fixedExampleURI = fmt.Sprintf("%s?_txlock=immediate", dbConfig.URI)
		} else {
			fixedExampleURI = fmt.Sprintf("%s&_txlock=immediate", dbConfig.URI)
		}
		m.Log.Warn().
			Str("fixed_uri_example", fixedExampleURI).
			Msg("Using SQLite without _txlock=immediate is not recommended")
	}
	var err error
	m.DB, err = dbutil.NewFromConfig("megabridge/"+m.Name, m.Config.Database, dbutil.ZeroLogger(m.Log.With().Str("db_section", "main").Logger()))
	if err != nil {
		m.Log.WithLevel(zerolog.FatalLevel).Err(err).Msg("Failed to initialize database connection")
		if sqlError := (&sqlite3.Error{}); errors.As(err, sqlError) && sqlError.Code == sqlite3.ErrCorrupt {
			os.Exit(18)
		}
		os.Exit(14)
	}
}

func validateConfig(m *mxmain.BridgeMain) error {
	switch {
	case m.Config.Homeserver.Address == "http://example.localhost:8008":
		return errors.New("homeserver.address not configured")
	case m.Config.Homeserver.Domain == "example.com":
		return errors.New("homeserver.domain not configured")
	case !bridgeconfig.AllowedHomeserverSoftware[m.Config.Homeserver.Software]:
		return errors.New("invalid value for homeserver.software (use `standard` if you don't know what the field is for)")
	case m.Config.AppService.ASToken == "This value is generated when generating the registration":
		return errors.New("appservice.as_token not configured. Did you forget to generate the registration? ")
	case m.Config.AppService.HSToken == "This value is generated when generating the registration":
		return errors.New("appservice.hs_token not configured. Did you forget to generate the registration? ")
	case m.Config.Database.URI == "postgres://user:password@host/database?sslmode=disable":
		return errors.New("database.uri not configured")
	case !m.Config.Bridge.Permissions.IsConfigured():
		return errors.New("bridge.permissions not configured")
	case !strings.Contains(m.Config.AppService.FormatUsername("1234567890"), "1234567890"):
		return errors.New("username template is missing user ID placeholder")
	default:
		cfgValidator, ok := m.Connector.(bridgev2.ConfigValidatingNetwork)
		if ok {
			return cfgValidator.ValidateConfig()
		}
		return nil
	}
}
