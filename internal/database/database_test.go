package database

import (
	"context"
	"database/sql"
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

func TestDialectFromDSN(t *testing.T) {
	tests := []struct {
		name       string
		connection string
		dialect    Dialect
		wantErr    bool
	}{
		{
			name:       "SQLite file DSN",
			connection: "file:catalog.db",
			dialect:    SQLite,
		},
		{
			name:       "Postgres URL",
			connection: "postgres://user:password@localhost:5432/catalog",
			dialect:    Postgres,
		},
		{
			name:       "PostgreSQL URL",
			connection: "postgresql://user:password@localhost:5432/catalog",
			dialect:    Postgres,
		},
		{
			name:       "Postgres connection string",
			connection: "host=localhost user=postgres dbname=catalog",
			dialect:    Postgres,
		},
		{
			name:       "bare SQLite filename",
			connection: "catalog.db",
			wantErr:    true,
		},
		{
			name:       "unsupported database URL",
			connection: "mysql://localhost/catalog",
			wantErr:    true,
		},
		{
			name:    "empty connection string",
			wantErr: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dialect, err := DialectFromDSN(test.connection)

			if test.wantErr {
				require.ErrorIs(t, err, ErrUnknownDialect)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.dialect, dialect)
		})
	}
}

func TestNewDatabaseRejectsUnsupportedDSN(t *testing.T) {
	database, err := NewDatabase("mysql://localhost/catalog", 10)

	assert.Nil(t, database)
	require.ErrorIs(t, err, ErrUnknownDialect)
}

// testConnection hands out a database of its own: these tests drop columns
// and indexes from the logs table, which would break the package main suite
// running at the same time against the database DATABASE_DSN points at.
func testConnection(t *testing.T) string {
	t.Helper()

	connection := os.Getenv("DATABASE_DSN")
	if connection == "" {
		return sqliteTestConnection(t)
	}

	dialect, err := DialectFromDSN(connection)
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

	database, err := NewDatabase(connection, 10)
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

func TestMigrationCreatesEventCreatedAtIndex(t *testing.T) {
	connection := testConnection(t)
	database := openDatabase(t, connection)

	const index = "idx_events_created_at"

	require.True(t, database.Migrator().HasIndex(&models.Event{}, index))
	require.NoError(t, database.Migrator().DropIndex(&models.Event{}, index))
	require.False(t, database.Migrator().HasIndex(&models.Event{}, index))

	event := models.Event{ID: "event-before-migration", Type: "create"}
	require.NoError(t, database.Create(&event).Error)

	migrated := openDatabase(t, connection)

	assert.True(t, migrated.Migrator().HasIndex(&models.Event{}, index))

	var stored models.Event

	require.NoError(t, migrated.First(&stored, "id = ?", event.ID).Error)
	assert.Equal(t, event.Type, stored.Type)
}

func TestNewDatabaseConnectionPool(t *testing.T) {
	connection := testConnection(t)
	dialect, err := DialectFromDSN(connection)
	require.NoError(t, err)

	const limit = 4
	database, err := NewDatabase(connection, limit)
	require.NoError(t, err)
	pool, err := database.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, pool.Close()) })

	if dialect == SQLite {
		assert.Zero(t, pool.Stats().MaxOpenConnections, "SQLite pooling stays unchanged")
		return
	}

	require.Equal(t, limit, pool.Stats().MaxOpenConnections)
	connections := make([]*sql.Conn, 0, limit)
	t.Cleanup(func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	})
	for range limit {
		connection, err := pool.Conn(context.Background())
		require.NoError(t, err)
		connections = append(connections, connection)
	}
	assert.Equal(t, limit, pool.Stats().InUse)
	for _, connection := range connections {
		require.NoError(t, connection.Close())
	}
	stats := pool.Stats()
	assert.Equal(t, limit, stats.OpenConnections)
	assert.Equal(t, limit, stats.Idle)
	assert.Zero(t, stats.MaxIdleClosed)
}

func TestNewDatabaseRejectsNonPositivePoolSize(t *testing.T) {
	for _, limit := range []int{0, -1} {
		database, err := NewDatabase("postgres://localhost/catalog", limit)
		assert.Nil(t, database)
		require.EqualError(t, err, "DATABASE_MAX_CONNECTIONS must be positive")
	}
}

func TestMigrationScopesExistingSoftwareURLs(t *testing.T) {
	connection := testConnection(t)
	database := openDatabase(t, connection)
	catalog := "catalog-migration"
	require.NoError(t, database.Create(&models.Catalog{ID: catalog, Name: "Migration"}).Error)
	for _, item := range []struct {
		id      string
		catalog *string
	}{{"owned", &catalog}, {"root", nil}} {
		scope := ""
		if item.catalog != nil {
			scope = *item.catalog
		}
		require.NoError(t, database.Create(&models.Software{
			ID: item.id, CatalogID: item.catalog, SoftwareURLID: item.id + "-url",
			URL:     models.SoftwareURL{ID: item.id + "-url", URL: "https://" + item.id + ".example.org", CatalogID: scope},
			Aliases: []models.SoftwareURL{{ID: item.id + "-alias", URL: "https://" + item.id + "-alias.example.org", CatalogID: scope}},
		}).Error)
	}
	// Recreate the old URL schema, which had only a global unique index.
	require.NoError(t, database.Migrator().DropIndex(&models.SoftwareURL{}, "idx_software_urls_catalog_url"))
	require.NoError(t, database.Exec("ALTER TABLE software_urls DROP COLUMN catalog_id").Error)
	require.NoError(t, database.Exec("CREATE UNIQUE INDEX idx_software_urls_url ON software_urls (url)").Error)
	migrated := openDatabase(t, connection)
	require.False(t, migrated.Migrator().HasIndex(&models.SoftwareURL{}, "idx_software_urls_url"))
	var urls []models.SoftwareURL
	require.NoError(t, migrated.Order("id").Find(&urls).Error)
	require.Len(t, urls, 4)
	for _, u := range urls {
		expected := ""
		if u.SoftwareID == "owned" {
			expected = catalog
		}
		assert.Equal(t, expected, u.CatalogID)
	}
	// Reopening must preserve the new ownership and allow the same URL in root.
	migrated = openDatabase(t, connection)
	require.NoError(t, migrated.Create(&models.SoftwareURL{ID: "shared", SoftwareID: "root", URL: "https://owned.example.org"}).Error)
	err := migrated.Create(&models.SoftwareURL{ID: "duplicate", SoftwareID: "root", URL: "https://owned.example.org"}).Error
	require.Error(t, err)
}

func TestMigrationRemovesWebhooksOfDeletedResources(t *testing.T) {
	connection := testConnection(t)

	database := openDatabase(t, connection)

	publisher := models.Publisher{ID: "5b1cf1a4-3f1e-4c4e-9b0e-6a2f0c7d8e91", Description: "Live publisher"}
	require.NoError(t, database.Session(&gorm.Session{SkipHooks: true}).Create(&publisher).Error)

	webhooks := []models.Webhook{
		{
			ID:         "b2f6c0de-7a43-4f0e-8c51-0e6d9a1f2b37",
			URL:        "https://example.org/live",
			EntityID:   publisher.ID,
			EntityType: "publishers",
		},
		{
			ID:         "c8d1a7e2-5b94-4c3f-a0e6-1f7b2d9c4e58",
			URL:        "https://example.org/deleted-publisher",
			EntityID:   "e4a9b3c1-8d2f-4a6e-b7c0-3f5d1e9a2b64",
			EntityType: "publishers",
		},
		{
			ID:         "d3e7f1a9-2c6b-4e8d-9a1f-5b0c7e3d6a42",
			URL:        "https://example.org/deleted-software",
			EntityID:   "f7c2e5a8-1b3d-4f9e-8a6c-2d4b0e7f1c93",
			EntityType: "software",
		},
		{
			ID:         "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d",
			URL:        "https://example.org/all-publishers",
			EntityType: "publishers",
		},
	}
	require.NoError(t, database.Session(&gorm.Session{SkipHooks: true}).Create(&webhooks).Error)

	migrated := openDatabase(t, connection)

	var remaining []string
	require.NoError(t, migrated.Model(&models.Webhook{}).Order("url").Pluck("url", &remaining).Error)

	assert.Equal(t, []string{"https://example.org/all-publishers", "https://example.org/live"}, remaining)
}

func TestMigrationRemovesLogsOfDeletedResources(t *testing.T) {
	connection := testConnection(t)

	database := openDatabase(t, connection)

	publisher := models.Publisher{ID: "0c3e8a51-6d2b-4f7a-9e14-8b5c2d7f0a36", Description: "Live publisher"}
	require.NoError(t, database.Session(&gorm.Session{SkipHooks: true}).Create(&publisher).Error)

	entityLog := func(id, message, entityType string, entityID *string) models.Log {
		return models.Log{ID: id, Message: message, EntityType: &entityType, EntityID: entityID}
	}
	deletedPublisher := "1e7b4c92-3a5f-4d8e-a0c6-9f2d5b8e1a47"
	deletedSoftware := "2f8c5da3-4b6e-4e9f-b1d7-0a3e6c9f2b58"
	deletedCatalog := "3a9d6eb4-5c7f-4fa0-82e8-1b4f7da03c69"

	logs := []models.Log{
		entityLog("4bae7fc5-6d80-4ab1-93f9-2c5a8eb14d7a", "live publisher", "publishers", &publisher.ID),
		entityLog("5cbf80d6-7e91-4bc2-a40a-3d6b9fc25e8b", "deleted publisher", "publishers", &deletedPublisher),
		entityLog("6dc091e7-8fa2-4cd3-b51b-4e7ca0d36f9c", "deleted software", "software", &deletedSoftware),
		entityLog("7ed1a2f8-90b3-4de4-862c-5f8db1e470ad", "deleted catalog", "catalogs", &deletedCatalog),
		entityLog("8fe2b309-a1c4-4ef5-973d-60a9c2f581be", "root catalog", "catalogs", nil),
		{ID: "90f3c41a-b2d5-4f06-a84e-71bad30692cf", Message: "no entity"},
	}
	require.NoError(t, database.Session(&gorm.Session{SkipHooks: true}).Create(&logs).Error)

	migrated := openDatabase(t, connection)

	var remaining []string
	require.NoError(t, migrated.Model(&models.Log{}).Order("message").Pluck("message", &remaining).Error)

	assert.Equal(t, []string{"live publisher", "no entity", "root catalog"}, remaining)

	var deleted int64
	require.NoError(t, migrated.Unscoped().Model(&models.Log{}).Where("deleted_at IS NOT NULL").Count(&deleted).Error)

	assert.Equal(t, int64(3), deleted)
}
