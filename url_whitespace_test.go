package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Normalization stores a space in a URL as %20, so a URL with a trailing
// space matched no existing row and created a duplicate software or
// publisher. Every URL a client sends is refused instead.
func TestURLsWithWhitespaceAreRefused(t *testing.T) {
	values := map[string]string{
		"trailing space":       "https://example.org/whitespace ",
		"inner space":          "https://example.org/white space",
		"trailing no-break":    "https://example.org/whitespace ",
		"trailing ideographic": "https://example.org/whitespace　",
	}

	tests := []struct {
		name        string
		method      string
		path        func(t *testing.T) string
		contentType string
		body        func(value string) any
		field       string
		rule        string
		// accepted replaces the URL for the request that has to pass.
		accepted string
	}{
		{
			name: "POST software url", method: "POST", path: fixedPath("/v1/software"),
			contentType: "application/json",
			body:        func(v string) any { return map[string]any{"url": v, "publiccodeYml": "-"} },
			field:       "url", rule: "http_url",
		},
		{
			name: "POST software alias", method: "POST", path: fixedPath("/v1/software"),
			contentType: "application/json",
			body: func(v string) any {
				return map[string]any{"url": "https://example.org/ws-post", "aliases": []string{v}, "publiccodeYml": "-"}
			},
			field: "aliases[0]", rule: "http_url",
		},
		{
			name: "merge PATCH software url", method: "PATCH", path: fixedPath("/v1/software/" + rootSoftwareID),
			contentType: "application/merge-patch+json",
			body:        func(v string) any { return map[string]any{"url": v} },
			field:       "url", rule: "http_url",
		},
		{
			name: "merge PATCH software alias", method: "PATCH", path: fixedPath("/v1/software/" + rootSoftwareID),
			contentType: "application/merge-patch+json",
			body:        func(v string) any { return map[string]any{"aliases": []string{v}} },
			field:       "aliases[0]", rule: "http_url",
		},
		{
			name: "JSON Patch software alias", method: "PATCH", path: fixedPath("/v1/software/" + rootSoftwareID),
			contentType: "application/json-patch+json",
			body: func(v string) any {
				return []map[string]any{{"op": "add", "path": "/aliases/0", "value": v}}
			},
			field: "aliases[0]", rule: "http_url",
		},
		{
			name: "POST publisher codeHosting", method: "POST", path: fixedPath("/v1/publishers"),
			contentType: "application/json",
			body: func(v string) any {
				return map[string]any{"description": "Whitespace", "codeHosting": []map[string]any{{"url": v}}}
			},
			field: "url", rule: "http_url",
		},
		{
			name: "merge PATCH publisher codeHosting", method: "PATCH", path: fixedPath(publisherPath + rootPublisherID),
			contentType: "application/merge-patch+json",
			body:        func(v string) any { return map[string]any{"codeHosting": []map[string]any{{"url": v}}} },
			field:       "url", rule: "http_url",
		},
		{
			name: "JSON Patch publisher codeHosting", method: "PATCH", path: fixedPath(publisherPath + rootPublisherID),
			contentType: "application/json-patch+json",
			body: func(v string) any {
				return []map[string]any{{"op": "add", "path": "/codeHosting/-", "value": map[string]any{"url": v}}}
			},
			field: "url", rule: "http_url",
		},
		{
			name: "POST webhook", method: "POST", path: fixedPath("/v1/software/" + rootSoftwareID + "/webhooks"),
			contentType: "application/json",
			body:        func(v string) any { return map[string]any{"url": v} },
			field:       "url", rule: "url",
			// An IP literal skips the DNS lookup of the target.
			accepted: "https://93.184.216.34/whitespace",
		},
		{
			name: "POST catalog source", method: "POST", path: fixedPath("/v1/catalogs"),
			contentType: "application/json",
			body: func(v string) any {
				return map[string]any{"name": "Whitespace", "sources": []map[string]any{{"url": v}}}
			},
			field: "url", rule: "url",
		},
		{
			name: "merge PATCH catalog source", method: "PATCH", path: newCatalogPath,
			contentType: "application/merge-patch+json",
			body:        func(v string) any { return map[string]any{"sources": []map[string]any{{"url": v}}} },
			field:       "url", rule: "url",
		},
	}

	for _, tt := range tests {
		for valueName, value := range values {
			t.Run(tt.name+" "+valueName, func(t *testing.T) {
				loadFixtures(t)

				status, response := sendURLLimitRequest(t, tt.method, tt.path(t), tt.contentType, tt.body(value))
				assert.Equal(t, 422, status, response)

				validationErrors, ok := response["validationErrors"].([]any)
				require.True(t, ok, response)
				require.Len(t, validationErrors, 1, response)

				validationError := validationErrors[0].(map[string]any)
				assert.Equal(t, tt.field, validationError["field"])
				assert.Equal(t, tt.rule, validationError["rule"])
			})
		}

		// The same request without the whitespace passes, so the 422
		// above comes from the whitespace alone.
		t.Run(tt.name+" without whitespace", func(t *testing.T) {
			loadFixtures(t)

			accepted := tt.accepted
			if accepted == "" {
				accepted = "https://whitespace.example.org/accepted"
			}

			status, response := sendURLLimitRequest(t, tt.method, tt.path(t), tt.contentType, tt.body(accepted))
			assert.Equal(t, 200, status, response)
		})
	}
}

func fixedPath(path string) func(t *testing.T) string {
	return func(*testing.T) string { return path }
}

func newCatalogPath(t *testing.T) string {
	t.Helper()

	status, created := sendURLLimitRequest(t, "POST", "/v1/catalogs", "application/json",
		map[string]any{"name": "Whitespace sources", "sources": []map[string]any{{"url": "https://example.org/source"}}})
	require.Equal(t, 200, status, created)

	return "/v1/catalogs/" + created["id"].(string)
}
