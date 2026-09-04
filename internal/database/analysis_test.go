package database

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
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

func (r analysisMergeRecord) AnalysisDocument() common.AnalysisData {
	return r.Analysis
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
			_, err := MergeAnalysis(gormdb, &target, patch)
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
	)
	require.NoError(t, err)
	require.Len(t, merged, 2)
	assert.JSONEq(t, `{"v":2,"fresh":true,"nullable":null}`, string(merged["scanner"]))
	assert.JSONEq(t, `{"v":1,"keep":true}`, string(merged["other"]))
}

func TestMergeAnalysisReadsBackTheUpdatedRow(t *testing.T) {
	gormdb := openAnalysisTestDatabase(t)
	firstID := "a-" + utils.UUIDv4()
	secondID := "z-" + utils.UUIDv4()

	require.NoError(t, gormdb.Create(&analysisMergeRecord{
		ID:       firstID,
		Analysis: common.AnalysisData{"scanner": json.RawMessage(`{"v":1,"row":"first"}`)},
	}).Error)
	require.NoError(t, gormdb.Create(&analysisMergeRecord{
		ID:       secondID,
		Analysis: common.AnalysisData{"scanner": json.RawMessage(`{"v":1,"row":"second"}`)},
	}).Error)
	t.Cleanup(func() {
		require.NoError(t, gormdb.Delete(&analysisMergeRecord{}, "id IN ?", []string{firstID, secondID}).Error)
	})

	target := analysisMergeRecord{ID: secondID}
	merged, err := MergeAnalysis(gormdb, &target, common.AnalysisData{"fresh": json.RawMessage(`{"v":2}`)})
	require.NoError(t, err)

	assert.JSONEq(t, `{"v":1,"row":"second"}`, string(merged["scanner"]))
	assert.JSONEq(t, `{"v":2}`, string(merged["fresh"]))

	var stored analysisMergeRecord

	require.NoError(t, gormdb.First(&stored, "id = ?", secondID).Error)
	assert.JSONEq(t, `{"v":1,"row":"second"}`, string(stored.Analysis["scanner"]))
	assert.JSONEq(t, `{"v":2}`, string(stored.Analysis["fresh"]))

	var untouched analysisMergeRecord

	require.NoError(t, gormdb.First(&untouched, "id = ?", firstID).Error)
	assert.Len(t, untouched.Analysis, 1)
	assert.JSONEq(t, `{"v":1,"row":"first"}`, string(untouched.Analysis["scanner"]))
}

func TestMergeAnalysisOnAMissingRowSendsNoEvent(t *testing.T) {
	gormdb := openDatabase(t, testConnection(t))
	softwareID := utils.UUIDv4()

	drainEventChan()
	t.Cleanup(drainEventChan)

	target := models.Software{ID: softwareID}
	merged, err := MergeAnalysis(
		gormdb,
		&target,
		common.AnalysisData{"scanner": json.RawMessage(`{"v":1}`)},
	)

	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	assert.Nil(t, merged)

	var events int64

	require.NoError(t, gormdb.Model(&models.Event{}).Where("entity_id = ?", softwareID).Count(&events).Error)
	assert.Zero(t, events)
	assert.Len(t, models.EventChan, 0)
}

func TestMergeAnalysisSendsOneUpdateEventAfterTheCommit(t *testing.T) {
	gormdb := openDatabase(t, testConnection(t))
	softwareID := utils.UUIDv4()
	urlID := utils.UUIDv4()

	require.NoError(t, gormdb.Create(&models.Software{
		ID:            softwareID,
		SoftwareURLID: urlID,
		URL: models.SoftwareURL{
			ID:         urlID,
			URL:        "https://example.com/" + softwareID,
			SoftwareID: softwareID,
		},
		PubliccodeYml: "publiccodeYmlVersion: 0.4",
	}).Error)

	drainEventChan()
	t.Cleanup(drainEventChan)

	target := models.Software{ID: softwareID}
	merged, err := MergeAnalysis(
		gormdb,
		&target,
		common.AnalysisData{"scanner": json.RawMessage(`{"v":1}`)},
	)
	require.NoError(t, err)
	assert.JSONEq(t, `{"v":1}`, string(merged["scanner"]))

	var events int64

	require.NoError(t, gormdb.Model(&models.Event{}).
		Where("entity_id = ? AND type = ?", softwareID, common.EventTypeUpdate).
		Count(&events).Error)
	assert.Equal(t, int64(1), events)

	require.Len(t, models.EventChan, 1)

	event := <-models.EventChan
	assert.Equal(t, softwareID, event.EntityID)
	assert.Equal(t, common.EventTypeUpdate, event.Type)
}

func drainEventChan() {
	for {
		select {
		case <-models.EventChan:
		default:
			return
		}
	}
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
