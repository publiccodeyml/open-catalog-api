package database

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type analysisMergeRecord struct {
	ID        string              `gorm:"primaryKey"`
	Analysis  common.AnalysisData `gorm:"type:jsonb"`
	UpdatedAt time.Time
}

func (analysisMergeRecord) TableName() string {
	return "analysis_merge_test_records"
}

func TestMergeAnalysisConcurrentNamespaces(t *testing.T) {
	gormdb := openAnalysisTestDatabase(t)
	recordID := utils.UUIDv4()
	record := analysisMergeRecord{ID: recordID}
	require.NoError(t, gormdb.Create(&record).Error)
	t.Cleanup(func() {
		require.NoError(t, gormdb.Delete(&analysisMergeRecord{}, "id = ?", recordID).Error)
	})

	patches := []common.AnalysisData{
		{"scanner-one": json.RawMessage(`{"v":1,"score":90}`)},
		{"scanner-two": json.RawMessage(`{"v":2,"grade":"A"}`)},
	}

	start := make(chan struct{})
	errorsChannel := make(chan error, len(patches))

	for _, patch := range patches {
		go func() {
			<-start

			target := analysisMergeRecord{ID: recordID}
			_, err := MergeAnalysis(gormdb, &target, patch, "id = ?", recordID)
			errorsChannel <- err
		}()
	}

	close(start)
	for range patches {
		require.NoError(t, <-errorsChannel)
	}

	var stored analysisMergeRecord
	require.NoError(t, gormdb.First(&stored, "id = ?", recordID).Error)
	require.Len(t, stored.Analysis, 2)
	assert.JSONEq(t, `{"v":1,"score":90}`, string(stored.Analysis["scanner-one"]))
	assert.JSONEq(t, `{"v":2,"grade":"A"}`, string(stored.Analysis["scanner-two"]))
}

func TestMergeAnalysisReplacesWholeNamespace(t *testing.T) {
	gormdb := openAnalysisTestDatabase(t)
	recordID := utils.UUIDv4()
	record := analysisMergeRecord{
		ID: recordID,
		Analysis: common.AnalysisData{
			"scanner": json.RawMessage(`{"v":1,"stale":true}`),
			"other":   json.RawMessage(`{"v":1,"keep":true}`),
		},
	}
	require.NoError(t, gormdb.Create(&record).Error)
	t.Cleanup(func() {
		require.NoError(t, gormdb.Delete(&analysisMergeRecord{}, "id = ?", recordID).Error)
	})

	target := analysisMergeRecord{ID: recordID}
	merged, err := MergeAnalysis(
		gormdb,
		&target,
		common.AnalysisData{"scanner": json.RawMessage(`{"v":2,"fresh":true,"nullable":null}`)},
		"id = ?",
		recordID,
	)
	require.NoError(t, err)
	require.Len(t, merged, 2)
	assert.JSONEq(t, `{"v":2,"fresh":true,"nullable":null}`, string(merged["scanner"]))
	assert.JSONEq(t, `{"v":1,"keep":true}`, string(merged["other"]))
}

func openAnalysisTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()

	dsn := os.Getenv("DATABASE_DSN")
	dialect := SQLite

	if dsn != "" {
		var err error

		dialect, err = Dialect(dsn)
		require.NoError(t, err)
	}

	var (
		gormdb *gorm.DB
		err    error
	)

	if dialect == Postgres {
		gormdb, err = gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	} else {
		dsn = "file:" + filepath.Join(t.TempDir(), "analysis.db") + "?_journal_mode=WAL&_busy_timeout=5000"
		gormdb, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	}
	require.NoError(t, err)
	require.NoError(t, gormdb.AutoMigrate(&analysisMergeRecord{}))

	sqldb, err := gormdb.DB()
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, sqldb.Close())
	})

	return gormdb
}
