package main

import (
	"encoding/json"
	"net/http"
	"testing"

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
			cachedApp.Use(newResponseCache())
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
