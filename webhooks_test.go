package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebhooksEndpoints(t *testing.T) {
	tests := []TestCase{
		// POST /software/webhooks
		{
			description: "POST webhook with too-short secret returns 422",
			query:       "POST /v1/software/webhooks",
			body:        `{"url": "https://example.org/receiver", "secret": "tooshort"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, "can't create Webhook", response["title"])
			},
		},
		{
			description: "POST webhook with valid 16-char secret returns 201",
			query:       "POST /v1/software/webhooks",
			body:        `{"url": "https://example.org/receiver", "secret": "1234567890abcdef"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assertUUID(t, response["id"])
				assert.Equal(t, "https://example.org/receiver", response["url"])
			},
		},
		{
			description: "POST webhook with the url of an existing one returns 409",
			query:       "POST /v1/software/webhooks",
			body:        `{"url": "https://2-b.example.org/receiver"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        409,
			expectedBody:        `{"title":"can't create Webhook","detail":"url already exists","status":409}`,
			expectedContentType: "application/problem+json",
		},
		{
			description: "POST webhook for a software with the url of an existing one returns 409",
			query:       "POST /v1/software/c5dec6fa-8a01-4881-9e7d-132770d4214d/webhooks",
			body:        `{"url": "https://1-b.example.org/receiver"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        409,
			expectedBody:        `{"title":"can't create Webhook","detail":"url already exists","status":409}`,
			expectedContentType: "application/problem+json",
		},
		// GET /webhooks/:id
		{
			query: "GET /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
			headers: map[string][]string{
				"Authorization": {goodToken},
			},
			expectedCode:        200,
			expectedBody:        `{"id":"007bc84a-7e2d-43a0-b7e1-a256d4114aa7","url":"https://1-b.example.org/receiver","format":"default","createdAt":"2017-05-01T00:00:00Z","updatedAt":"2017-05-01T00:00:00Z"}`,
			expectedContentType: "application/json",
		},
		{
			description: "Non-existent webhook",
			query:       "GET /v1/webhooks/eea19c82-0449-11ed-bd84-d8bbc146d165",
			headers: map[string][]string{
				"Authorization": {goodToken},
			},
			expectedCode: 404,
			expectedBody: `{"title":"can't get Webhook","detail":"Webhook was not found","status":404}`,

			expectedContentType: "application/problem+json",
		},

		// GET /software/webhooks
		{
			description:         "GET webhooks with invalid format for page[size] query param",
			query:               "GET /v1/software/webhooks?page[size]=NOT_AN_INT",
			headers:             map[string][]string{"Authorization": {goodToken}},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			validateFunc: func(t *testing.T, response map[string]interface{}) {
				assert.Equal(t, "can't get Webhooks", response["title"])
				assert.Equal(t, "page[size] must be an integer", response["detail"])
			},
		},

		// GET /software/:id/webhooks
		{
			description:         "GET webhooks of a software with invalid format for page[size] query param",
			query:               "GET /v1/software/9f135268-a37e-4ead-96ec-e4a24bb9344a/webhooks?page[size]=NOT_AN_INT",
			headers:             map[string][]string{"Authorization": {goodToken}},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			validateFunc: func(t *testing.T, response map[string]interface{}) {
				assert.Equal(t, "can't get Webhooks", response["title"])
				assert.Equal(t, "page[size] must be an integer", response["detail"])
			},
		},

		// PATCH /webhooks/:id
		{
			description: "PATCH webhook is not supported",
			query:       "PATCH /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
			body:        `{"url": "https://new.example.org/receiver"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        405,
			expectedContentType: "application/problem+json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, "Method Not Allowed", response["title"])
			},
		},
		// DELETE /webhooks/:id
		{
			description: "Delete non-existent webhook",
			query:       "DELETE /v1/webhooks/NO_SUCH_WEBHOOK",
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode: 404,
			// This error is different from because it's returned directly from Fiber's
			// route constraints, so we don't need to hit the database to find the resource
			// because we already know that's not a valid webhook id looking at its format.
			expectedBody:        `{"title":"Not Found","detail":"Cannot DELETE /v1/webhooks/NO_SUCH_WEBHOOK","status":404}`,
			expectedContentType: "application/problem+json",
		},
		{
			description: "DELETE webhook with bad authentication",
			query:       "DELETE /v1/webhooks/1702cd06-fffb-4d20-8f55-73e2a00ee052",
			headers: map[string][]string{
				"Authorization": {badToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        401,
			expectedBody:        `{"title":"token authentication failed","status":401}`,
			expectedContentType: "application/problem+json",
		},
		{
			query: "DELETE /v1/webhooks/24bc1b5d-fe81-47be-9d55-910f820bdd04",
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        204,
			expectedBody:        "",
			expectedContentType: "",
		},
	}

	runTestCases(t, tests)
}

func TestWebhookReadsRequireAuthenticationAndBypassCache(t *testing.T) {
	loadFixtures(t)

	paths := []string{
		"/v1/software/webhooks",
		"/v1/software/c5dec6fa-8a01-4881-9e7d-132770d4214d/webhooks",
		"/v1/publishers/webhooks",
		"/v1/publishers/47807e0c-0613-4aea-9917-5455cc6eddad/webhooks",
		"/v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
	}

	for _, path := range paths {
		request, err := newTestRequest(http.MethodGet, path, nil)
		require.NoError(t, err)
		request.Header.Set("Authorization", goodToken)

		response, err := app.Test(request, -1)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, response.StatusCode, path)
		require.NoError(t, response.Body.Close())

		request, err = newTestRequest(http.MethodGet, path, nil)
		require.NoError(t, err)

		response, err = app.Test(request, -1)
		require.NoError(t, err)
		assert.Equal(t, http.StatusUnauthorized, response.StatusCode, path)
		require.NoError(t, response.Body.Close())
	}

	request, err := newTestRequest(http.MethodGet, "/v1/software", nil)
	require.NoError(t, err)

	response, err := app.Test(request, -1)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })

	assert.Equal(t, http.StatusOK, response.StatusCode)
}

func TestWebhooksPostDBChecks(t *testing.T) {
	t.Run("POST webhook persists URL and entity fields to DB", func(t *testing.T) {
		loadFixtures(t)

		const url = "https://db-check.example.org/receiver"

		req, err := newTestRequest("POST", "/v1/software/webhooks", strings.NewReader(`{"url":"`+url+`","secret":"1234567890abcdef"}`))
		require.NoError(t, err)
		req.Header = map[string][]string{
			"Authorization": {goodToken},
			"Content-Type":  {"application/json"},
		}

		res, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, 200, res.StatusCode)

		var body map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&body))
		webhookID := body["id"].(string)

		assert.Equal(t, url, dbValue(t, "webhooks", "url", "id", webhookID))
		assert.Equal(t, "software", dbValue(t, "webhooks", "entity_type", "id", webhookID))
		assert.Equal(t, "", dbValue(t, "webhooks", "entity_id", "id", webhookID))
	})
}

func TestWebhooksDeleteDBChecks(t *testing.T) {
	t.Run("DELETE webhook removes the row from DB", func(t *testing.T) {
		loadFixtures(t)

		const webhookID = "24bc1b5d-fe81-47be-9d55-910f820bdd04"

		req, err := newTestRequest("DELETE", "/v1/webhooks/"+webhookID, nil)
		require.NoError(t, err)
		req.Header = map[string][]string{"Authorization": {goodToken}}

		res, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, 204, res.StatusCode)

		assert.Equal(t, 0, dbCount(t, "webhooks", "id", webhookID))
	})

	t.Run("DELETE of a missing webhook records no event", func(t *testing.T) {
		loadFixtures(t)

		const webhookID = "00000000-dead-beef-0000-0000000000bb"

		req, err := newTestRequest("DELETE", "/v1/webhooks/"+webhookID, nil)
		require.NoError(t, err)
		req.Header = map[string][]string{"Authorization": {goodToken}}

		res, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, 404, res.StatusCode)

		assert.Equal(t, 0, dbCount(t, "events", "entity_id", webhookID))
	})
}
