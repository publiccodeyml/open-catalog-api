package database

import (
	"errors"
	"fmt"
	"log"
	"strings"

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

	if err := migrateModels(database); err != nil {
		return nil, fmt.Errorf("database migration error: %w", err)
	}

	// Workaround until #72 (proper migrations): GIN index on analysis for
	// per-namespace queries. SQLite doesn't support GIN, PostgreSQL only.
	if dialect != SQLite {
		sql := "CREATE INDEX IF NOT EXISTS idx_software_analysis_gin ON software USING GIN (analysis)"
		if err := database.Exec(sql).Error; err != nil {
			return nil, fmt.Errorf("can't create analysis GIN index: %w", err)
		}

		sql = "CREATE INDEX IF NOT EXISTS idx_catalogs_analysis_gin ON catalogs USING GIN (analysis)"
		if err := database.Exec(sql).Error; err != nil {
			return nil, fmt.Errorf("can't create catalog analysis GIN index: %w", err)
		}
	}

	return database, nil
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

		database, err = gorm.Open(postgres.Open(connection), &gorm.Config{
			PrepareStmt: true,
			Logger:      logger.Default.LogMode(logger.Silent),
		})
	default:
		return nil, ErrUnknownDialect
	}

	if err != nil {
		return nil, fmt.Errorf("can't open database: %w", err)
	}

	return database, nil
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
