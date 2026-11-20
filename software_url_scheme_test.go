package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A catalog frontend renders the software URL as a link, so a URL that is
// not http(s) would run or open something else on click.
func TestSoftwareURLsNeedHTTPScheme(t *testing.T) {
	refused := []string{
		"javascript:alert(document.cookie)",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/passwd",
		"mailto:a@example.org",
		"ftp://example.org/repo",
		"http:///no-host",
	}

	for _, value := range refused {
		t.Run("POST url "+value, func(t *testing.T) {
			loadFixtures(t)

			status, response := sendURLLimitRequest(t, "POST", "/v1/software", "application/json",
				map[string]any{"url": value, "publiccodeYml": "-"})
			assert.Equal(t, 422, status, response)
			assertSchemeError(t, response, "url")
		})

		t.Run("POST alias "+value, func(t *testing.T) {
			loadFixtures(t)

			status, response := sendURLLimitRequest(t, "POST", "/v1/software", "application/json",
				map[string]any{"url": "https://example.org/scheme-post", "aliases": []string{value}, "publiccodeYml": "-"})
			assert.Equal(t, 422, status, response)
			assertSchemeError(t, response, "aliases[0]")
		})

		t.Run("PATCH "+value, func(t *testing.T) {
			loadFixtures(t)

			status, created := sendURLLimitRequest(t, "POST", "/v1/software", "application/json",
				map[string]any{"url": "https://example.org/scheme-patch", "publiccodeYml": "-"})
			require.Equal(t, 200, status, created)

			path := "/v1/software/" + created["id"].(string)

			status, response := sendURLLimitRequest(t, "PATCH", path, "application/merge-patch+json",
				map[string]any{"url": value})
			assert.Equal(t, 422, status, response)
			assertSchemeError(t, response, "url")

			status, response = sendURLLimitRequest(t, "PATCH", path, "application/merge-patch+json",
				map[string]any{"aliases": []string{value}})
			assert.Equal(t, 422, status, response)
			assertSchemeError(t, response, "aliases[0]")

			status, response = sendURLLimitRequest(t, "PATCH", path, "application/json-patch+json",
				[]map[string]any{{"op": "add", "path": "/aliases/-", "value": value}})
			assert.Equal(t, 422, status, response)
			assertSchemeError(t, response, "aliases[0]")
		})
	}

	t.Run("http and https in any letter case", func(t *testing.T) {
		loadFixtures(t)

		status, response := sendURLLimitRequest(t, "POST", "/v1/software", "application/json",
			map[string]any{
				"url":           "HTTPS://example.org/scheme-case",
				"aliases":       []string{"http://example.org/scheme-old", "Http://example.org/scheme-older"},
				"publiccodeYml": "-",
			})
		assert.Equal(t, 200, status, response)
	})
}

func assertSchemeError(t *testing.T, response map[string]any, field string) {
	t.Helper()

	validationErrors, ok := response["validationErrors"].([]any)
	require.True(t, ok, response)
	require.Len(t, validationErrors, 1)

	validationError := validationErrors[0].(map[string]any)
	assert.Equal(t, field, validationError["field"])
	assert.Equal(t, "http_url", validationError["rule"])
}
