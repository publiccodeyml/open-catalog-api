package database

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
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

	gormdb := openDatabase(t, testConnection(t))
	require.NoError(t, gormdb.AutoMigrate(&analysisMergeRecord{}))

	return gormdb
}

func TestMergeAnalysisKeepsEveryStoredJSONType(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		stored    string
	}{
		{"string", "s", `"just a string"`},
		{"integer", "i", `1`},
		{"float", "f", `1.5`},
		{"true", "t", `true`},
		{"false", "b", `false`},
		{"null", "n", `null`},
		{"array", "a", `[1,"x",null]`},
		{"nested object", "o", `{"v":1,"nested":{"k":[true]}}`},
		{"object", "normal", `{"v":1}`},
	}

	gormdb := openAnalysisTestDatabase(t)
	recordID := utils.UUIDv4()
	seeded := make(common.AnalysisData, len(tests))

	for _, test := range tests {
		seeded[test.namespace] = json.RawMessage(test.stored)
	}

	require.NoError(t, gormdb.Create(&analysisMergeRecord{ID: recordID, Analysis: seeded}).Error)
	t.Cleanup(func() {
		require.NoError(t, gormdb.Delete(&analysisMergeRecord{}, "id = ?", recordID).Error)
	})

	target := analysisMergeRecord{ID: recordID}
	merged, err := MergeAnalysis(gormdb, &target, common.AnalysisData{"fresh": json.RawMessage(`{"v":2}`)})
	require.NoError(t, err)

	var stored analysisMergeRecord

	require.NoError(t, gormdb.First(&stored, "id = ?", recordID).Error)
	require.Len(t, stored.Analysis, len(tests)+1)
	assert.JSONEq(t, `{"v":2}`, string(stored.Analysis["fresh"]))
	assert.Equal(t, decodeAnalysis(t, stored.Analysis), decodeAnalysis(t, merged))

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t,
				decodeValue(t, json.RawMessage(test.stored)),
				decodeValue(t, stored.Analysis[test.namespace]),
			)
		})
	}
}

func decodeAnalysis(t *testing.T, analysis common.AnalysisData) map[string]any {
	t.Helper()

	decoded := make(map[string]any, len(analysis))
	for namespace, value := range analysis {
		decoded[namespace] = decodeValue(t, value)
	}

	return decoded
}

func decodeValue(t *testing.T, value json.RawMessage) any {
	t.Helper()

	var decoded any

	require.NoError(t, json.Unmarshal(value, &decoded))

	return decoded
}

func TestMergeAnalysisKeepsTheStoredNamespaceWhenOnlyTheTimestampDiffers(t *testing.T) {
	gormdb := openAnalysisTestDatabase(t)
	recordID := utils.UUIDv4()

	// The stored and the incoming namespace use the same key order, as the
	// API writes them, so the textual comparison on SQLite matches.
	require.NoError(t, gormdb.Create(&analysisMergeRecord{
		ID: recordID,
		Analysis: common.AnalysisData{
			"scanner": json.RawMessage(`{"v":1,"score":90,"t":"2020-01-01T00:00:00Z"}`),
		},
	}).Error)
	t.Cleanup(func() {
		require.NoError(t, gormdb.Delete(&analysisMergeRecord{}, "id = ?", recordID).Error)
	})

	target := analysisMergeRecord{ID: recordID}
	merged, err := MergeAnalysis(gormdb, &target, common.AnalysisData{
		"scanner": json.RawMessage(`{"v":1,"score":90,"t":"2026-09-04T10:00:00Z"}`),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"v":1,"score":90,"t":"2020-01-01T00:00:00Z"}`, string(merged["scanner"]))

	var stored analysisMergeRecord

	require.NoError(t, gormdb.First(&stored, "id = ?", recordID).Error)
	assert.JSONEq(t, `{"v":1,"score":90,"t":"2020-01-01T00:00:00Z"}`, string(stored.Analysis["scanner"]))
}

func TestMergeAnalysisWritesTheIncomingNamespaceWhenTheValueDiffers(t *testing.T) {
	gormdb := openAnalysisTestDatabase(t)
	recordID := utils.UUIDv4()

	require.NoError(t, gormdb.Create(&analysisMergeRecord{
		ID: recordID,
		Analysis: common.AnalysisData{
			"scanner": json.RawMessage(`{"v":1,"score":50,"t":"2020-01-01T00:00:00Z"}`),
		},
	}).Error)
	t.Cleanup(func() {
		require.NoError(t, gormdb.Delete(&analysisMergeRecord{}, "id = ?", recordID).Error)
	})

	target := analysisMergeRecord{ID: recordID}
	merged, err := MergeAnalysis(gormdb, &target, common.AnalysisData{
		"scanner": json.RawMessage(`{"v":1,"score":90,"t":"2026-09-04T10:00:00Z"}`),
	})
	require.NoError(t, err)
	assert.JSONEq(t, `{"v":1,"score":90,"t":"2026-09-04T10:00:00Z"}`, string(merged["scanner"]))

	var stored analysisMergeRecord

	require.NoError(t, gormdb.First(&stored, "id = ?", recordID).Error)
	assert.JSONEq(t, `{"v":1,"score":90,"t":"2026-09-04T10:00:00Z"}`, string(stored.Analysis["scanner"]))
}
