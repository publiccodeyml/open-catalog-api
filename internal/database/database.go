package database

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	SQLite   = "sqlite"
	Postgres = "postgres"
)

var ErrUnknownDialect = errors.New(
	`unrecognized database DSN: expected a "file:" SQLite DSN, a "postgres://" URL or a key=value connection string`,
)

// Dialect reports which database a DSN points at. Everything that opens
// a connection goes through here, so the application and the test
// harness can't disagree and end up talking to two different databases.
func Dialect(connection string) (string, error) {
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

func NewDatabase(connection string) (*gorm.DB, error) {
	dialect, err := Dialect(connection)
	if err != nil {
		return nil, err
	}

	var database *gorm.DB

	switch dialect {
	case SQLite:
		log.Println("using SQLite database")

		database, err = gorm.Open(sqlite.Open(connection), &gorm.Config{})
	default:
		log.Println("using Postgres database")

		database, err = gorm.Open(postgres.Open(connection), &gorm.Config{
			PrepareStmt: true,
			// Disable logging in production
			Logger: logger.Default.LogMode(logger.Silent),
		})
	}

	if err != nil {
		return nil, fmt.Errorf("can't open database: %w", err)
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

	// Migrate logs only if there is no "entity" column yet, which should mean when the database
	// is empty.
	// This is a workaround for https://github.com/go-gorm/gorm/issues/5534 where GORM
	// fails to migrate an existing generated column on PostgreSQL if it already exists.
	var entity sql.NullString
	if database.Raw("SELECT entity FROM logs LIMIT 1").Scan(&entity).Error != nil {
		if err := database.AutoMigrate(&models.Log{}); err != nil {
			return fmt.Errorf("can't migrate model \"Log\": %w", err)
		}
	}

	return nil
}
