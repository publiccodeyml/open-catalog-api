package main

import (
	"io"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestHealthEndpoints(t *testing.T) {
	tests := []TestCase{
		{
			query:               "GET /livez",
			expectedCode:        200,
			expectedBody:        "OK",
			expectedContentType: "text/plain; charset=utf-8",
		},
		{
			query:               "GET /readyz",
			expectedCode:        200,
			expectedBody:        "OK",
			expectedContentType: "text/plain; charset=utf-8",
		},
		{
			query:               "GET /v1/status",
			expectedCode:        204,
			expectedBody:        "",
			expectedContentType: "",
		},
	}

	runTestCases(t, tests)
}

func TestStatusCacheHeaders(t *testing.T) {
	loadFixtures(t)

	req, err := newTestRequest("GET", "/v1/status", nil)
	require.NoError(t, err)

	res, err := app.Test(req, -1)
	require.NoError(t, err)

	assert.Equal(t, 204, res.StatusCode)
	assert.Equal(t, "no-cache", res.Header.Get("Cache-Control"))
}

func TestHealthEndpointsWhenDatabaseUnavailable(t *testing.T) {
	unavailableDB, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)

	pool, err := unavailableDB.DB()
	require.NoError(t, err)
	require.NoError(t, pool.Close())

	healthApp := fiber.New()
	healthApp.Use(newHealthcheck(unavailableDB))
	t.Cleanup(func() { require.NoError(t, healthApp.Shutdown()) })

	tests := []struct {
		path                string
		expectedCode        int
		expectedBody        string
		expectedContentType string
	}{
		{
			path:                "/livez",
			expectedCode:        200,
			expectedBody:        "OK",
			expectedContentType: "text/plain; charset=utf-8",
		},
		{
			path:                "/readyz",
			expectedCode:        503,
			expectedBody:        "Service Unavailable",
			expectedContentType: "text/plain; charset=utf-8",
		},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			req, err := newTestRequest("GET", test.path, nil)
			require.NoError(t, err)

			res, err := healthApp.Test(req, -1)
			require.NoError(t, err)

			assert.Equal(t, test.expectedCode, res.StatusCode)
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			assert.Equal(t, test.expectedBody, string(body))
			assert.Equal(t, test.expectedContentType, res.Header.Get("Content-Type"))
		})
	}
}
