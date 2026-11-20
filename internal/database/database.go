package database

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

type Dialect string

const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

var ErrUnknownDialect = errors.New(
	`unrecognized database DSN: expected a "file:" SQLite DSN, a "postgres://" URL or a key=value connection string`,
)

var (
	errUnknownGormDialect = errors.New("unrecognized gorm driver name")
	errInvalidPoolSize    = errors.New("DATABASE_MAX_CONNECTIONS must be positive")
)

// DialectFromDSN reports which database a DSN points at. Everything that
// opens a connection goes through here, so the application and the test
// harness can't disagree and end up talking to two different databases.
func DialectFromDSN(connection string) (Dialect, error) {
	switch {
	case strings.HasPrefix(connection, "file:"):
		return SQLite, nil
	case strings.HasPrefix(connection, "postgres://"),
		strings.HasPrefix(connection, "postgresql://"),
		strings.Contains(connection, "="):
		return Postgres, nil
	}

	return "", fmt.Errorf("%w: %q", ErrUnknownDialect, connection)
}

// DialectOf reports the Dialect an already open *gorm.DB is talking to, read
// from the driver name gorm reports for it. It is the one place that maps a
// gorm driver name back to a Dialect, so a request handler and NewDatabase
// read the same fact instead of each guessing from the connection.
func DialectOf(gormdb *gorm.DB) (Dialect, error) {
	switch name := gormdb.Name(); name {
	case string(SQLite):
		return SQLite, nil
	case string(Postgres):
		return Postgres, nil
	default:
		return "", fmt.Errorf("%w: %q", errUnknownGormDialect, name)
	}
}

func NewDatabase(connection string, maxConnections int) (*gorm.DB, error) {
	dialect, err := DialectFromDSN(connection)
	if err != nil {
		return nil, err
	}

	if maxConnections <= 0 {
		return nil, errInvalidPoolSize
	}

	database, err := openDatabaseConnection(connection, dialect)
	if err != nil {
		return nil, err
	}

	if dialect == Postgres {
		pool, err := database.DB()
		if err != nil {
			return nil, fmt.Errorf("can't get database pool: %w", err)
		}

		pool.SetMaxOpenConns(maxConnections)
		pool.SetMaxIdleConns(maxConnections)
	}

	if err := migrate(database, dialect); err != nil {
		return nil, fmt.Errorf("database migration error: %w", err)
	}

	return database, nil
}

// migrationLockKey is the PostgreSQL advisory lock taken around the
// migration. Its value only has to differ from other advisory locks on
// the same database.
const migrationLockKey int64 = 0x6f63615f6d6967

// migrate brings the schema up to date. On PostgreSQL several replicas
// can start at the same time, and GORM checks for a table, column or
// index before creating it, so two of them create the same one and the
// slower one fails. They take turns under an advisory lock instead,
// held on one pinned connection because the lock belongs to the
// session. SQLite already serializes writers.
func migrate(database *gorm.DB, dialect Dialect) error {
	if dialect != Postgres {
		return migrateSchema(database, dialect)
	}

	err := database.Connection(func(pinned *gorm.DB) error {
		// Connection hands out a statement every call would build on,
		// a new session starts each call from a clean one.
		conn := pinned.Session(&gorm.Session{NewDB: true})

		if err := conn.Exec("SELECT pg_advisory_lock(?)", migrationLockKey).Error; err != nil {
			return fmt.Errorf("can't take the migration lock: %w", err)
		}

		defer func() {
			// An error here means the connection is gone, and
			// PostgreSQL releases the lock with the session.
			if err := conn.Exec("SELECT pg_advisory_unlock(?)", migrationLockKey).Error; err != nil {
				log.Printf("can't unlock the database migration: %s", err)
			}
		}()

		return migrateSchema(conn, dialect)
	})
	if err != nil {
		return fmt.Errorf("can't migrate on one connection: %w", err)
	}

	return nil
}

func migrateSchema(database *gorm.DB, dialect Dialect) error {
	if err := migrateModels(database); err != nil {
		return err
	}

	// Workaround until #72 (proper migrations): GIN index on analysis for
	// per-namespace queries. SQLite doesn't support GIN, PostgreSQL only.
	if dialect != SQLite {
		sql := "CREATE INDEX IF NOT EXISTS idx_software_analysis_gin ON software USING GIN (analysis)"
		if err := database.Exec(sql).Error; err != nil {
			return fmt.Errorf("can't create analysis GIN index: %w", err)
		}

		sql = "CREATE INDEX IF NOT EXISTS idx_catalogs_analysis_gin ON catalogs USING GIN (analysis)"
		if err := database.Exec(sql).Error; err != nil {
			return fmt.Errorf("can't create catalog analysis GIN index: %w", err)
		}

		createLogSearchIndex(database)
	}

	return nil
}

// LogSearchIndex is the trigram index serving ?search on the log list,
// which matches LOWER(message) LIKE '%term%'. A B-tree can't serve a
// pattern with a leading wildcard, so without it every search that
// matches few logs reads the whole table.
const LogSearchIndex = "idx_logs_message_trgm"

// createLogSearchIndex creates LogSearchIndex. pg_trgm is a trusted
// extension, so the database owner can create it, but a role without
// the CREATE privilege on the database can't. The API still works
// without the index, only log searches get slower, so a failure is
// logged and startup goes on.
func createLogSearchIndex(database *gorm.DB) {
	if database.Migrator().HasIndex(&models.Log{}, LogSearchIndex) {
		return
	}

	if err := database.Exec("CREATE EXTENSION IF NOT EXISTS pg_trgm").Error; err != nil {
		log.Printf("log searches run without an index, can't create the pg_trgm extension: %s", err)

		return
	}

	sql := "CREATE INDEX " + LogSearchIndex + " ON logs USING GIN (LOWER(message) gin_trgm_ops)"
	if err := database.Exec(sql).Error; err != nil {
		log.Printf("log searches run without an index, can't create %s: %s", LogSearchIndex, err)

		return
	}

	// The planner knows nothing about LOWER(message) until the table is
	// analyzed, and autovacuum may not get to a large logs table for a
	// long time. Until then it guesses that a search matches many rows
	// and walks created_at instead of using the index.
	if err := database.Exec("ANALYZE logs").Error; err != nil {
		log.Printf("can't analyze logs after creating %s: %s", LogSearchIndex, err)
	}
}

func openDatabaseConnection(connection string, dialect Dialect) (*gorm.DB, error) {
	var (
		database *gorm.DB
		err      error
	)

	switch dialect {
	case SQLite:
		log.Println("using SQLite database")

		database, err = gorm.Open(sqlite.Open(connection), &gorm.Config{})
	case Postgres:
		log.Println("using Postgres database")

		var dialector *postgres.Dialector

		dialector, err = postgresDialector(connection)
		if err != nil {
			return nil, err
		}

		database, err = gorm.Open(dialector, &gorm.Config{
			Logger: logger.Default.LogMode(logger.Silent),
		})
	default:
		return nil, ErrUnknownDialect
	}

	if err != nil {
		return nil, fmt.Errorf("can't open database: %w", err)
	}

	return database, nil
}

// queryExecModeParam is the pgx DSN parameter choosing how queries run.
const queryExecModeParam = "default_query_exec_mode"

// postgresDialector opens PostgreSQL without keeping prepared statements
// around. A prepared SELECT * keeps the columns of the table as they
// were when it was prepared, and PostgreSQL refuses to run it once
// another instance adds a column, as a release does in AutoMigrate
// while the old replicas still serve. GORM's PrepareStmt never drops
// such a statement, and the pgx statement cache drops it only after
// the query fails on each connection. pgx describes every query
// instead, unless the DSN picks its own default_query_exec_mode.
func postgresDialector(connection string) (*postgres.Dialector, error) {
	config, err := pgconn.ParseConfig(connection)
	if err != nil {
		return nil, fmt.Errorf("can't parse the database DSN: %w", err)
	}

	var options []stdlib.OptionOpenDB

	if _, ok := config.RuntimeParams[queryExecModeParam]; !ok {
		options = append(options, stdlib.OptionBeforeConnect(func(_ context.Context, config *pgx.ConnConfig) error {
			config.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec

			return nil
		}))
	}

	return &postgres.Dialector{Config: &postgres.Config{
		DSN:          connection,
		OptionOpenDB: options,
	}}, nil
}

func migrateModels(database *gorm.DB) error {
	for _, model := range []any{
		&models.Catalog{},
		&models.CatalogSource{},
		&models.Publisher{},
		&models.Event{},
		&models.CodeHosting{},
		&models.Software{},
		&models.SoftwareURL{},
		// After Software: the bundles_software join table references it.
		&models.Bundle{},
		&models.Webhook{},
	} {
		if err := database.AutoMigrate(model); err != nil {
			return fmt.Errorf("can't migrate %T: %w", model, err)
		}
	}

	if err := migrateSoftwareURLs(database); err != nil {
		return err
	}

	if err := removeOrphanResourceWebhooks(database); err != nil {
		return err
	}

	if err := migrateLogs(database); err != nil {
		return err
	}

	return removeOrphanResourceLogs(database)
}

// removeOrphanResourceLogs soft deletes the logs left behind by publishers,
// software and catalogs deleted before their deletion removed them too.
// Logs without an entity_id, like the root catalog ones, are kept.
func removeOrphanResourceLogs(database *gorm.DB) error {
	for _, table := range []string{
		models.Publisher{}.TableName(),
		models.Software{}.TableName(),
		models.Catalog{}.TableName(),
	} {
		result := database.Session(&gorm.Session{SkipHooks: true}).
			Where("entity_type = ? AND entity_id IS NOT NULL", table).
			Where("NOT EXISTS (?)", database.Table(table).Select("1").Where(table+".id = logs.entity_id")).
			Delete(&models.Log{})
		if result.Error != nil {
			return fmt.Errorf("can't remove the logs of deleted %s: %w", table, result.Error)
		}

		if result.RowsAffected > 0 {
			log.Printf("removed %d logs of deleted %s", result.RowsAffected, table)
		}
	}

	return nil
}

// removeOrphanResourceWebhooks deletes the webhooks left behind by
// publishers and software deleted before their deletion removed them too.
// Type wide webhooks have an empty entity_id and are kept.
func removeOrphanResourceWebhooks(database *gorm.DB) error {
	for _, table := range []string{
		models.Publisher{}.TableName(),
		models.Software{}.TableName(),
	} {
		result := database.Session(&gorm.Session{SkipHooks: true}).
			Where("entity_type = ? AND entity_id <> ''", table).
			Where("NOT EXISTS (?)", database.Table(table).Select("1").Where(table+".id = webhooks.entity_id")).
			Delete(&models.Webhook{})
		if result.Error != nil {
			return fmt.Errorf("can't remove the webhooks of deleted %s: %w", table, result.Error)
		}

		if result.RowsAffected > 0 {
			log.Printf("removed %d webhooks of deleted %s", result.RowsAffected, table)
		}
	}

	return nil
}

const entityField = "Entity"

// migrateLogs can't call AutoMigrate on a logs table that already has the
// generated "entity" column: GORM fails on it on PostgreSQL
// (https://github.com/go-gorm/gorm/issues/5534). Such a table gets its
// missing columns and indexes added one at a time instead, so that a change
// to models.Log still reaches a database created by an earlier version.
func migrateLogs(database *gorm.DB) error {
	migrator := database.Migrator()

	if !migrator.HasColumn(&models.Log{}, entityField) {
		if err := database.AutoMigrate(&models.Log{}); err != nil {
			return fmt.Errorf("can't migrate model \"Log\": %w", err)
		}

		return nil
	}

	statement := &gorm.Statement{DB: database}
	if err := statement.Parse(&models.Log{}); err != nil {
		return fmt.Errorf("can't parse model \"Log\": %w", err)
	}

	if err := addMissingLogColumns(migrator, statement.Schema); err != nil {
		return err
	}

	return createMissingLogIndexes(migrator, statement.Schema)
}

func addMissingLogColumns(migrator gorm.Migrator, logs *schema.Schema) error {
	for _, field := range logs.Fields {
		if field.Name == entityField || migrator.HasColumn(&models.Log{}, field.Name) {
			continue
		}

		if err := migrator.AddColumn(&models.Log{}, field.Name); err != nil {
			return fmt.Errorf("can't add column %q to \"logs\": %w", field.DBName, err)
		}
	}

	return nil
}

func createMissingLogIndexes(migrator gorm.Migrator, logs *schema.Schema) error {
	for _, index := range logs.ParseIndexes() {
		if migrator.HasIndex(&models.Log{}, index.Name) {
			continue
		}

		if err := migrator.CreateIndex(&models.Log{}, index.Name); err != nil {
			return fmt.Errorf("can't create index %q on \"logs\": %w", index.Name, err)
		}
	}

	return nil
}

// migrateSoftwareURLs replaces global URL uniqueness after the new column
// and composite index exist. Keep the old index until all owners are copied.
func migrateSoftwareURLs(database *gorm.DB) error {
	const oldIndex = "idx_software_urls_url"

	if !database.Migrator().HasIndex(&models.SoftwareURL{}, oldIndex) {
		return nil
	}

	if err := database.Transaction(func(tran *gorm.DB) error {
		if err := tran.Exec(`UPDATE software_urls SET catalog_id = COALESCE(
			(SELECT catalog_id FROM software WHERE software.id = software_urls.software_id), '')`).Error; err != nil {
			return err
		}

		return tran.Migrator().DropIndex(&models.SoftwareURL{}, oldIndex)
	}); err != nil {
		return fmt.Errorf("can't migrate software URL ownership: %w", err)
	}

	return nil
}
