package bridgeentry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"regexp"
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
	ctx := m.Log.WithContext(context.Background())
	if err = migrateEmptyBridgeIDs(ctx, m.DB, bridgeID, m.Log); err != nil {
		m.Log.Fatal().Err(err).Str("bridge_id", string(bridgeID)).Msg("Failed to migrate empty bridge IDs")
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

var safeSQLIdentifier = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func migrateEmptyBridgeIDs(ctx context.Context, db *dbutil.Database, target networkid.BridgeID, log *zerolog.Logger) error {
	if db == nil || target == "" {
		return nil
	}
	tables, err := bridgeIDTables(ctx, db)
	if err != nil {
		return err
	}
	if len(tables) == 0 {
		return nil
	}

	type migrationPlan struct {
		table      string
		emptyCount int64
	}
	plans := make([]migrationPlan, 0, len(tables))
	for _, table := range tables {
		quoted, err := quoteIdentifier(table)
		if err != nil {
			return err
		}
		var emptyCount int64
		if err = db.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE bridge_id=''", quoted)).Scan(&emptyCount); err != nil {
			return err
		}
		if emptyCount == 0 {
			continue
		}
		var targetCount int64
		if err = db.QueryRow(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE bridge_id=$1", quoted), target).Scan(&targetCount); err != nil {
			return err
		}
		if targetCount > 0 {
			return fmt.Errorf("table %s has both empty and canonical bridge IDs; refusing ambiguous migration", table)
		}
		plans = append(plans, migrationPlan{table: table, emptyCount: emptyCount})
	}
	if len(plans) == 0 {
		return nil
	}

	if log != nil {
		log.Warn().
			Str("bridge_id", string(target)).
			Int("table_count", len(plans)).
			Msg("Migrating rows persisted with empty bridge_id to canonical bridge ID")
	}

	return db.DoTxn(ctx, nil, func(ctx context.Context) error {
		if db.Dialect == dbutil.SQLite {
			// Rewrite all related bridge_id columns as one logical migration and defer
			// FK validation until commit so parent/child tables can move together.
			if _, err := db.Exec(ctx, "PRAGMA defer_foreign_keys = ON"); err != nil {
				return fmt.Errorf("enable deferred foreign keys: %w", err)
			}
		}
		for _, plan := range plans {
			quoted, err := quoteIdentifier(plan.table)
			if err != nil {
				return err
			}
			res, err := db.Exec(ctx, fmt.Sprintf("UPDATE %s SET bridge_id=$1 WHERE bridge_id=''", quoted), target)
			if err != nil {
				return fmt.Errorf("migrate %s: %w", plan.table, err)
			}
			if log != nil {
				if affected, affErr := res.RowsAffected(); affErr == nil && affected > 0 {
					log.Info().
						Str("bridge_id", string(target)).
						Str("table", plan.table).
						Int64("rows", affected).
						Msg("Migrated empty bridge_id rows")
				}
			}
		}
		return nil
	})
}

func bridgeIDTables(ctx context.Context, db *dbutil.Database) ([]string, error) {
	switch db.Dialect {
	case dbutil.SQLite:
		return sqliteBridgeIDTables(ctx, db)
	case dbutil.Postgres:
		return postgresBridgeIDTables(ctx, db)
	default:
		return nil, fmt.Errorf("unsupported database dialect %s", db.Dialect.String())
	}
}

func sqliteBridgeIDTables(ctx context.Context, db *dbutil.Database) ([]string, error) {
	rows, err := db.Query(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			return nil, err
		}
		quoted, err := quoteIdentifier(table)
		if err != nil {
			return nil, err
		}
		colRows, err := db.Query(ctx, fmt.Sprintf("PRAGMA table_info(%s)", quoted))
		if err != nil {
			return nil, err
		}
		hasBridgeID := false
		for colRows.Next() {
			var cid int
			var name, colType string
			var notNull, pk int
			var dflt sql.NullString
			if err = colRows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
				_ = colRows.Close()
				return nil, err
			}
			if name == "bridge_id" {
				hasBridgeID = true
			}
		}
		if closeErr := colRows.Close(); closeErr != nil && err == nil {
			return nil, closeErr
		}
		if hasBridgeID {
			tables = append(tables, table)
		}
	}
	return tables, rows.Err()
}

func postgresBridgeIDTables(ctx context.Context, db *dbutil.Database) ([]string, error) {
	rows, err := db.Query(ctx, `
		SELECT DISTINCT table_name
		FROM information_schema.columns
		WHERE table_schema='public' AND column_name='bridge_id'
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			return nil, err
		}
		tables = append(tables, table)
	}
	return tables, rows.Err()
}

func quoteIdentifier(name string) (string, error) {
	if !safeSQLIdentifier.MatchString(name) {
		return "", fmt.Errorf("unsafe SQL identifier %q", name)
	}
	return `"` + name + `"`, nil
}
