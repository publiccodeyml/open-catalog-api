package database

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const deletedAtIndex = "idx_logs_deleted_at"

// testConnection hands out a database of its own: these tests drop columns
// and indexes from the logs table, which would break the package main suite
// running at the same time against the database DATABASE_DSN points at.
func testConnection(t *testing.T) string {
	t.Helper()

	connection := os.Getenv("DATABASE_DSN")
	if connection == "" {
		return sqliteTestConnection(t)
	}

	dialect, err := Dialect(connection)
	require.NoError(t, err)

	if dialect == SQLite {
		return sqliteTestConnection(t)
	}

	return postgresTestConnection(t, connection)
}

func sqliteTestConnection(t *testing.T) string {
	t.Helper()

	return "file:" + filepath.Join(t.TempDir(), "migration-test.db")
}

func postgresTestConnection(t *testing.T, adminConnection string) string {
	t.Helper()

	parsed, err := url.Parse(adminConnection)
	if err != nil || parsed.Host == "" {
		t.Skip("DATABASE_DSN is not a Postgres URL, can't create a scratch database")
	}

	admin, err := gorm.Open(postgres.Open(adminConnection), &gorm.Config{})
	require.NoError(t, err, "can't connect to the database in DATABASE_DSN")

	const scratch = "logmigration_test"

	require.NoError(t, admin.Exec("DROP DATABASE IF EXISTS "+scratch).Error)
	require.NoError(t, admin.Exec("CREATE DATABASE "+scratch).Error)

	t.Cleanup(func() {
		admin.Exec("DROP DATABASE IF EXISTS " + scratch)

		if pool, err := admin.DB(); err == nil {
			_ = pool.Close()
		}
	})

	parsed.Path = "/" + scratch

	return parsed.String()
}

func openDatabase(t *testing.T, connection string) *gorm.DB {
	t.Helper()

	database, err := NewDatabase(connection)
	require.NoError(t, err)

	t.Cleanup(func() {
		if pool, err := database.DB(); err == nil {
			_ = pool.Close()
		}
	})

	return database
}

func TestMigrationAddsAColumnMissingFromTheLogsTable(t *testing.T) {
	connection := testConnection(t)

	database := openDatabase(t, connection)

	require.True(t, database.Migrator().HasColumn(&models.Log{}, "UpdatedAt"))
	require.NoError(t, database.Exec("ALTER TABLE logs DROP COLUMN updated_at").Error)
	require.False(t, database.Migrator().HasColumn(&models.Log{}, "UpdatedAt"))

	migrated := openDatabase(t, connection)

	assert.True(t, migrated.Migrator().HasColumn(&models.Log{}, "UpdatedAt"))
}

func TestMigrationRecreatesAnIndexMissingFromTheLogsTable(t *testing.T) {
	connection := testConnection(t)

	database := openDatabase(t, connection)

	require.True(t, database.Migrator().HasIndex(&models.Log{}, deletedAtIndex))
	require.NoError(t, database.Exec("DROP INDEX "+deletedAtIndex).Error)
	require.False(t, database.Migrator().HasIndex(&models.Log{}, deletedAtIndex))

	migrated := openDatabase(t, connection)

	assert.True(t, migrated.Migrator().HasIndex(&models.Log{}, deletedAtIndex))
}

func TestMigrationKeepsTheGeneratedEntityColumn(t *testing.T) {
	connection := testConnection(t)

	database := openDatabase(t, connection)

	require.NoError(t, database.Exec("ALTER TABLE logs DROP COLUMN updated_at").Error)

	migrated := openDatabase(t, connection)

	entityID := "0ea4f79f-2eb5-4d40-b31f-a5a6bf1d0b4c"
	entityType := "software"
	logID := "d3b56b17-e4f4-4e4c-a9f3-49dcf6b58d3f"

	require.NoError(t, migrated.Create(&models.Log{
		ID:         logID,
		Message:    "a log message",
		EntityID:   &entityID,
		EntityType: &entityType,
	}).Error)

	var stored models.Log

	require.NoError(t, migrated.First(&stored, "id = ?", logID).Error)

	assert.Equal(t, "/"+entityType+"/"+entityID, stored.Entity)
}
