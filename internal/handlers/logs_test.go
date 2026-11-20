package handlers

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newLogDB(t *testing.T, maxOpen int) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "logs.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&models.Log{}))

	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(maxOpen)

	t.Cleanup(func() { _ = pool.Close() })

	return db
}

func TestSearchSlotsAreHalfThePool(t *testing.T) {
	assert.Nil(t, NewLog(newLogDB(t, 0)).searches, "a pool without a limit has nothing to protect")
	assert.Equal(t, 1, cap(NewLog(newLogDB(t, 1)).searches))
	assert.Equal(t, 5, cap(NewLog(newLogDB(t, 10)).searches))
	assert.Equal(t, 5, cap(NewLog(newLogDB(t, 11)).searches))
}

func TestLogSearchesLeaveConnectionsToOtherRequests(t *testing.T) {
	handler := NewLog(newLogDB(t, 4))

	app := fiber.New(fiber.Config{ErrorHandler: common.CustomErrorHandler})
	app.Get("/logs", handler.GetLogs)

	get := func(query string) (int, string) {
		t.Helper()

		res, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/logs"+query, nil))
		require.NoError(t, err)

		defer res.Body.Close()

		return res.StatusCode, res.Header.Get(fiber.HeaderRetryAfter)
	}

	// Two searches hold the two slots of a pool of four.
	handler.searches <- struct{}{}
	handler.searches <- struct{}{}

	status, retryAfter := get("?search=zq1")
	assert.Equal(t, fiber.StatusServiceUnavailable, status)
	assert.Equal(t, "1", retryAfter)

	status, _ = get("")
	assert.Equal(t, fiber.StatusOK, status, "a list without ?search doesn't take a slot")

	status, _ = get("?filter=zq1")
	assert.Equal(t, fiber.StatusOK, status)

	<-handler.searches

	status, _ = get("?search=zq1")
	assert.Equal(t, fiber.StatusOK, status)
	assert.Len(t, handler.searches, 1, "a finished search gives its slot back")
}
