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

		// PATCH /webhooks/:id
		{
			query: "PATCH /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
			body:  `{"url": "https://new.example.org/receiver"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, "007bc84a-7e2d-43a0-b7e1-a256d4114aa7", response["id"])
				assert.Equal(t, "https://new.example.org/receiver", response["url"])
				assert.Equal(t, "2017-05-01T00:00:00Z", response["createdAt"])

				assertRFC3339(t, response["updatedAt"])
				assertOnlyKeys(t, response, "id", "url", "format", "createdAt", "updatedAt")
			},
		},
		{
			description: "PATCH webhook with non-normalized URL",
			query:       "PATCH /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
			body:        `{"url": "https://www.new.example.org/receiver/"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, "https://new.example.org/receiver", response["url"])
			},
		},
		{
			description: "PATCH webhook without format keeps the stored format",
			query:       "PATCH /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
			body:        `{"url": "https://new.example.org/receiver"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]interface{}) {
				assert.Equal(t, "default", response["format"])
			},
		},
		{
			description: "PATCH webhook with unknown format",
			query:       "PATCH /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
			body:        `{"url": "https://new.example.org/receiver", "format": "carrier-pigeon"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			validateFunc: func(t *testing.T, response map[string]interface{}) {
				assert.Equal(t, `can't update Webhook`, response["title"])

				validationErrors := response["validationErrors"].([]interface{})
				assert.Equal(t, 1, len(validationErrors))

				firstValidationError := validationErrors[0].(map[string]interface{})
				assert.Equal(t, "format", firstValidationError["field"])
			},
		},
		{
			description: "PATCH webhook - wrong token",
			query:       "PATCH /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
			body:        `{"url": "https://new.example.org/receiver"}`,
			headers: map[string][]string{
				"Authorization": {badToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        401,
			expectedBody:        `{"title":"token authentication failed","status":401}`,
			expectedContentType: "application/problem+json",
		},
		{
			description: "PATCH /v1/webhooks with invalid JSON",
			query:       "PATCH /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
			body:        `INVALID_JSON`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        400,
			expectedContentType: "application/problem+json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, `can't update Webhook`, response["title"])
				assert.Equal(t, "invalid or malformed JSON", response["detail"])
			},
		},
		{
			description: "PATCH /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7 with JSON with extra fields",
			query:       "PATCH /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
			body:        `{"url": "https://new.example.org/receiver", "EXTRA_FIELD": "extra field not in schema"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Webhook","detail":"unknown field in JSON input","status":422}`,
		},
		{
			description: "PATCH webhook with validation errors",
			query:       "PATCH /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
			body:        `{"url": "INVALID_URL"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, `can't update Webhook`, response["title"])
				assert.Equal(t, "invalid format: url is invalid", response["detail"])

				assert.IsType(t, []any{}, response["validationErrors"])

				validationErrors := response["validationErrors"].([]any)
				assert.Equal(t, 1, len(validationErrors))

				firstValidationError := validationErrors[0].(map[string]any)

				for key := range firstValidationError {
					assert.Contains(t, []string{"field", "rule", "value"}, key)
				}
			},
		},
		{
			description: "PATCH /v1/webhooks with empty body",
			query:       "PATCH /v1/webhooks/007bc84a-7e2d-43a0-b7e1-a256d4114aa7",
			body:        "",
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        400,
			expectedContentType: "application/problem+json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, `can't update Webhook`, response["title"])
				assert.Equal(t, "invalid or malformed JSON", response["detail"])
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

func patchWebhook(t *testing.T, id, body string) *http.Response {
	t.Helper()

	req, err := newTestRequest("PATCH", "/v1/webhooks/"+id, strings.NewReader(body))
	require.NoError(t, err)

	req.Header = map[string][]string{
		"Authorization": {goodToken},
		"Content-Type":  {"application/json"},
	}

	res, err := app.Test(req, -1)
	require.NoError(t, err)

	return res
}

func TestPatchWebhookPersistsSecret(t *testing.T) {
	loadFixtures(t)

	const webhookID = "007bc84a-7e2d-43a0-b7e1-a256d4114aa7"

	res := patchWebhook(t, webhookID,
		`{"url": "https://new.example.org/receiver", "secret": "rotated-secret-long-enough"}`)
	assert.Equal(t, 200, res.StatusCode)

	var response map[string]interface{}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&response))

	assertOnlyKeys(t, response, "id", "url", "format", "createdAt", "updatedAt")

	assert.Equal(t, "rotated-secret-long-enough",
		dbValue(t, "webhooks", "secret", "id", webhookID))
}

func TestPatchWebhookWithoutSecretKeepsTheStoredOne(t *testing.T) {
	loadFixtures(t)

	const webhookID = "007bc84a-7e2d-43a0-b7e1-a256d4114aa7"

	res := patchWebhook(t, webhookID,
		`{"url": "https://new.example.org/receiver", "secret": "stored-secret-long-enough"}`)
	require.Equal(t, 200, res.StatusCode)

	res = patchWebhook(t, webhookID, `{"url": "https://other.example.org/receiver"}`)
	assert.Equal(t, 200, res.StatusCode)

	assert.Equal(t, "stored-secret-long-enough",
		dbValue(t, "webhooks", "secret", "id", webhookID))
	assert.Equal(t, "https://other.example.org/receiver",
		dbValue(t, "webhooks", "url", "id", webhookID))
}
