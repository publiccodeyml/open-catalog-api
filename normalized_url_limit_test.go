package main

import (
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Each Cyrillic letter is stored percent-encoded, six characters long.
func cyrillicURL(letters int) string {
	return "https://example.org/" + strings.Repeat("ж", letters)
}

func sendURLLimitRequest(t *testing.T, method, path, contentType string, body any) (int, map[string]any) {
	t.Helper()

	var reader io.Reader

	switch typed := body.(type) {
	case string:
		reader = strings.NewReader(typed)
	default:
		encoded, err := json.Marshal(typed)
		require.NoError(t, err)

		reader = strings.NewReader(string(encoded))
	}

	req, err := newTestRequest(method, path, reader)
	require.NoError(t, err)
	req.Header.Set("Authorization", goodToken)
	req.Header.Set("Content-Type", contentType)

	res, err := app.Test(req, -1)
	require.NoError(t, err)
	defer res.Body.Close()

	var response map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&response))

	return res.StatusCode, response
}

func TestURLLimitsApplyToTheStoredURL(t *testing.T) {
	// 20 + 45*6 = 290 characters once stored, 65 as sent.
	tooLong := cyrillicURL(45)

	tests := []struct {
		name   string
		path   string
		body   map[string]any
		detail string
	}{
		{
			"software url", "/v1/software",
			map[string]any{"url": tooLong, "publiccodeYml": "-"},
			"url does not meet its size limits (too long)",
		},
		{
			"software alias", "/v1/software",
			map[string]any{"url": "https://example.org/alias-limit", "aliases": []string{tooLong}, "publiccodeYml": "-"},
			"aliases[0] does not meet its size limits (too long)",
		},
		{
			"publisher code hosting url", "/v1/publishers",
			map[string]any{"description": "URL limit", "codeHosting": []map[string]any{{"url": tooLong}}},
			"url does not meet its size limits (too long)",
		},
		{
			// 20 + 339*6 = 2054 characters once stored.
			"catalog source url", "/v1/catalogs",
			map[string]any{"name": "URL limit", "sources": []map[string]any{{"url": cyrillicURL(339)}}},
			"url does not meet its size limits (too long)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadFixtures(t)

			status, response := sendURLLimitRequest(t, "POST", tt.path, "application/json", tt.body)
			assert.Equal(t, 422, status)
			assert.Contains(t, response["detail"], tt.detail)

			validationErrors, ok := response["validationErrors"].([]any)
			require.True(t, ok)
			require.Len(t, validationErrors, 1)
			assert.Equal(t, "max", validationErrors[0].(map[string]any)["rule"])
		})
	}
}

func TestURLAtTheStoredLimitStaysPatchable(t *testing.T) {
	// 20 + 39*6 = 254 characters once stored.
	atLimit := cyrillicURL(39)

	t.Run("software", func(t *testing.T) {
		loadFixtures(t)

		status, created := sendURLLimitRequest(t, "POST", "/v1/software", "application/json",
			map[string]any{"url": atLimit, "aliases": []string{atLimit + "a"}, "publiccodeYml": "-"})
		require.Equal(t, 200, status)
		require.Len(t, created["url"], 254)

		path := "/v1/software/" + created["id"].(string)

		status, response := sendURLLimitRequest(t, "PATCH", path, "application/merge-patch+json",
			`{"publiccodeYml":"y"}`)
		assert.Equal(t, 200, status, response)

		status, response = sendURLLimitRequest(t, "PATCH", path, "application/merge-patch+json",
			map[string]any{"url": created["url"]})
		assert.Equal(t, 200, status, response)

		status, response = sendURLLimitRequest(t, "PATCH", path, "application/json-patch+json",
			`[{"op":"replace","path":"/publiccodeYml","value":"z"}]`)
		assert.Equal(t, 200, status, response)
	})

	t.Run("publisher", func(t *testing.T) {
		loadFixtures(t)

		status, created := sendURLLimitRequest(t, "POST", "/v1/publishers", "application/json",
			map[string]any{"description": "URL at the limit", "codeHosting": []map[string]any{{"url": atLimit}}})
		require.Equal(t, 200, status)

		path := "/v1/publishers/" + created["id"].(string)

		status, response := sendURLLimitRequest(t, "PATCH", path, "application/merge-patch+json",
			`{"description":"Still patchable"}`)
		assert.Equal(t, 200, status, response)

		status, response = sendURLLimitRequest(t, "PATCH", path, "application/json-patch+json",
			`[{"op":"replace","path":"/description","value":"Patchable again"}]`)
		assert.Equal(t, 200, status, response)
	})
}
