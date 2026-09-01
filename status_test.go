package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
