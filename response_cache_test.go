package main

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResponseCacheSeparatesPathAndQuery(t *testing.T) {
	loadFixtures(t)

	publishers := []struct {
		id            string
		alternativeID string
		target        string
	}{
		{"2ded32eb-c45e-4167-9166-a44e18b8adde", "a", "/v1/publishers/a?b=c"},
		{"47807e0c-0613-4aea-9917-5455cc6eddad", "ab=c", "/v1/publishers/ab=c"},
	}
	for _, publisher := range publishers {
		require.NoError(t, gormDB.Model(&models.Publisher{}).
			Where("id = ?", publisher.id).
			Update("alternative_id", publisher.alternativeID).Error)
	}

	for _, order := range [][]int{{0, 1}, {1, 0}} {
		t.Run(publishers[order[0]].target+" first", func(t *testing.T) {
			cachedApp := fiber.New(fiber.Config{ErrorHandler: common.CustomErrorHandler})
			cachedApp.Use(newResponseCache(responseCacheMaxBytes, responseCacheMaxEntries, time.Now))
			setupHandlers(cachedApp, gormDB)

			for _, cacheStatus := range []string{"miss", "hit"} {
				for _, index := range order {
					publisher := publishers[index]
					request, err := newTestRequest(http.MethodGet, publisher.target, nil)
					require.NoError(t, err)

					response, err := cachedApp.Test(request, -1)
					require.NoError(t, err)
					require.Equal(t, http.StatusOK, response.StatusCode)

					var body map[string]any
					err = json.NewDecoder(response.Body).Decode(&body)
					require.NoError(t, response.Body.Close())
					require.NoError(t, err)
					assert.Equal(t, publisher.id, body["id"], publisher.target)
					assert.Equal(t, publisher.alternativeID, body["alternativeId"], publisher.target)
					assert.Equal(t, cacheStatus, response.Header.Get("X-Cache"), publisher.target)
				}
			}
		})
	}
}

func TestResponseCacheBounds(t *testing.T) {
	loadFixtures(t)

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	newApp := func(maxBytes, maxEntries int, now *time.Time) *fiber.App {
		app := fiber.New(fiber.Config{ErrorHandler: common.CustomErrorHandler})
		app.Use(newResponseCache(maxBytes, maxEntries, func() time.Time { return *now }))
		setupHandlers(app, gormDB)

		return app
	}

	get := func(t *testing.T, app *fiber.App, query string) (string, int) {
		t.Helper()

		request, err := newTestRequest(http.MethodGet, "/v1/publishers/2ded32eb-c45e-4167-9166-a44e18b8adde?bound="+query, nil)
		require.NoError(t, err)

		response, err := app.Test(request, -1)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode)

		body, err := io.ReadAll(response.Body)
		require.NoError(t, response.Body.Close())
		require.NoError(t, err)

		return response.Header.Get("X-Cache"), len(body)
	}

	sizeNow := start
	_, size := get(t, newApp(responseCacheMaxBytes, responseCacheMaxEntries, &sizeNow), "size")
	require.Positive(t, size)

	type step struct{ query, cacheStatus string }

	// expire is a step that moves the clock past the admission window
	// instead of sending a request.
	const expire = ""

	full := []step{
		{"a", "miss"}, {"a", "hit"}, {"b", "unreachable"}, {"b", "unreachable"}, {"a", "hit"},
		{expire, ""}, {"b", "miss"}, {"b", "hit"}, {"c", "unreachable"},
	}

	tests := []struct {
		name       string
		maxBytes   int
		maxEntries int
		steps      []step
	}{
		{
			// Room for one body: later query strings aren't stored until
			// the cached one expires.
			name:       "bytes",
			maxBytes:   size,
			maxEntries: responseCacheMaxEntries,
			steps:      full,
		},
		{
			// Room for one entry, same as above.
			name:       "entries",
			maxBytes:   responseCacheMaxBytes,
			maxEntries: 1,
			steps:      full,
		},
		{
			name:       "body over the byte limit",
			maxBytes:   size - 1,
			maxEntries: responseCacheMaxEntries,
			steps:      []step{{"a", "unreachable"}, {"a", "unreachable"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := start
			app := newApp(test.maxBytes, test.maxEntries, &now)

			for _, step := range test.steps {
				if step.query == expire {
					now = now.Add(responseCacheExpiration + responseCacheStorageDelay)

					continue
				}

				cacheStatus, _ := get(t, app, step.query)
				assert.Equal(t, step.cacheStatus, cacheStatus, step.query)
			}
		})
	}
}

func TestCacheAdmission(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	admission := newCacheAdmission(100, 2, 10*time.Second)

	assert.True(t, admission.admit(start, 10))
	assert.True(t, admission.admit(start.Add(time.Second), 10))
	assert.False(t, admission.admit(start.Add(2*time.Second), 0), "full")
	assert.False(t, admission.admit(start.Add(10*time.Second-1), 0), "first entry not expired yet")
	assert.True(t, admission.admit(start.Add(10*time.Second), 90), "first entry expired")
	assert.False(t, admission.admit(start.Add(10*time.Second), 0), "full again")

	assert.False(t, admission.admit(start.Add(11*time.Second), 91), "second entry expired, over the bytes")
	assert.True(t, admission.admit(start.Add(11*time.Second), 10), "second entry expired")
	assert.Equal(t, 100, admission.bytes)

	assert.True(t, admission.admit(start.Add(30*time.Second), 100), "all expired")
	assert.Equal(t, 1, admission.count)
	assert.Equal(t, 100, admission.bytes)

	assert.False(t, newCacheAdmission(100, 0, time.Second).admit(start, 0))
	assert.False(t, newCacheAdmission(0, 1, time.Second).admit(start, 1))
}
