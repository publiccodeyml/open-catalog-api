package database

import (
	"testing"
	"time"

	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const day = 24 * time.Hour

func newEventDatabase(t *testing.T) *gorm.DB {
	t.Helper()

	gormdb, err := NewDatabase("file:" + t.Name() + "?mode=memory&cache=shared")
	require.NoError(t, err)

	return gormdb
}

func createEvent(t *testing.T, gormdb *gorm.DB, age time.Duration) string {
	t.Helper()

	event := models.Event{
		ID:         utils.UUIDv4(),
		Type:       common.EventTypeCreate,
		EntityType: "software",
		EntityID:   utils.UUIDv4(),
		CreatedAt:  time.Now().Add(-age),
	}

	require.NoError(t, gormdb.Create(&event).Error)

	return event.ID
}

// countEvents counts unscoped, so a soft deleted row still counts and a
// purge passes only when the row is gone.
func countEvents(t *testing.T, gormdb *gorm.DB, where string, args ...any) int64 {
	t.Helper()

	var count int64

	query := gormdb.Unscoped().Model(&models.Event{})
	if where != "" {
		query = query.Where(where, args...)
	}

	assert.NoError(t, query.Count(&count).Error)

	return count
}

func eventExists(t *testing.T, gormdb *gorm.DB, id string) bool {
	t.Helper()

	return countEvents(t, gormdb, "id = ?", id) > 0
}

func TestPurgeEvents(t *testing.T) {
	gormdb := newEventDatabase(t)

	recent := createEvent(t, gormdb, day)
	within := createEvent(t, gormdb, 20*day)
	expired := createEvent(t, gormdb, 40*day)

	purged, err := PurgeEvents(gormdb, 30*day)
	require.NoError(t, err)

	assert.Equal(t, int64(1), purged)
	assert.Equal(t, int64(2), countEvents(t, gormdb, ""))
	assert.True(t, eventExists(t, gormdb, recent))
	assert.True(t, eventExists(t, gormdb, within))
	assert.False(t, eventExists(t, gormdb, expired))
}

func TestPurgeEventsDeletesTheSoftDeletedRows(t *testing.T) {
	gormdb := newEventDatabase(t)

	expired := createEvent(t, gormdb, 40*day)
	require.NoError(t, gormdb.Delete(&models.Event{}, "id = ?", expired).Error)
	require.Equal(t, int64(1), countEvents(t, gormdb, ""))

	purged, err := PurgeEvents(gormdb, 30*day)
	require.NoError(t, err)

	assert.Equal(t, int64(1), purged)
	assert.Equal(t, int64(0), countEvents(t, gormdb, ""))
}

func TestStartEventPurgeDeletesOnStartup(t *testing.T) {
	gormdb := newEventDatabase(t)

	createEvent(t, gormdb, 40*day)
	createEvent(t, gormdb, day)

	stop := StartEventPurge(gormdb, 30*day, time.Hour)
	t.Cleanup(stop)

	assert.Eventually(t, func() bool {
		return countEvents(t, gormdb, "") == 1
	}, time.Second, 10*time.Millisecond)
}

func TestStartEventPurgeKeepsEveryEventWithoutRetention(t *testing.T) {
	gormdb := newEventDatabase(t)

	createEvent(t, gormdb, 40*day)

	stop := StartEventPurge(gormdb, 0, time.Hour)
	t.Cleanup(stop)

	assert.Never(t, func() bool {
		return countEvents(t, gormdb, "") == 0
	}, 200*time.Millisecond, 10*time.Millisecond)
}
