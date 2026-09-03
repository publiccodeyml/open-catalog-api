package main

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventActor covers the audit trail: every write leaves an event row
// carrying the subject of the token that authenticated the request.
func TestEventActor(t *testing.T) {
	const (
		deletablePublisherID = "15fda7c4-6bbf-4387-8f89-258c1e6fafb1"
		deletableSoftwareID  = "11e101c4-f989-4cc4-a665-63f9f34e83f6"
	)

	tests := []struct {
		description string
		method      string
		path        string
		body        string
		subject     string
		// entityID is the row the event is expected for. Left empty on a
		// POST, which only learns the id from the response.
		entityID      string
		expectedCode  int
		expectedActor string
	}{
		{
			description:   "POST publisher",
			method:        http.MethodPost,
			path:          "/v1/publishers",
			body:          `{"description":"actor test publisher","codeHosting":[{"url":"https://actor-test.example.org/repo"}]}`,
			subject:       "crawler",
			expectedCode:  200,
			expectedActor: "crawler",
		},
		{
			description:   "POST software",
			method:        http.MethodPost,
			path:          "/v1/software",
			body:          `{"publiccodeYml":"-","url":"https://actor-test.example.org/software"}`,
			subject:       "crawler",
			expectedCode:  200,
			expectedActor: "crawler",
		},
		{
			description:   "POST publisher in a catalog",
			method:        http.MethodPost,
			path:          "/v1/catalogs/" + italiaID + "/publishers",
			body:          `{"description":"actor test catalog publisher","codeHosting":[{"url":"https://actor-test.example.org/catalog-repo"}]}`,
			subject:       "curator",
			expectedCode:  200,
			expectedActor: "curator",
		},
		{
			description:   "PATCH publisher",
			method:        http.MethodPatch,
			path:          publisherPath + italiaPublisherID,
			body:          `{"description":"actor test patched description"}`,
			subject:       "editor",
			entityID:      italiaPublisherID,
			expectedCode:  200,
			expectedActor: "editor",
		},
		{
			description:   "POST catalog",
			method:        http.MethodPost,
			path:          "/v1/catalogs",
			body:          `{"name":"actor test catalog","sources":[{"url":"https://actor-test.example.org/catalog"}]}`,
			subject:       "curator",
			expectedCode:  200,
			expectedActor: "curator",
		},
		{
			description:   "PATCH catalog",
			method:        http.MethodPatch,
			path:          catalogPath + italiaID,
			body:          `{"name":"actor test patched catalog"}`,
			subject:       "curator",
			entityID:      italiaID,
			expectedCode:  200,
			expectedActor: "curator",
		},
		{
			description:   "POST bundle",
			method:        http.MethodPost,
			path:          "/v1/bundles",
			body:          `{"name":"actor test bundle","softwareIds":["` + swissSoftwareID + `"]}`,
			subject:       "curator",
			expectedCode:  200,
			expectedActor: "curator",
		},
		{
			description:   "PATCH bundle",
			method:        http.MethodPatch,
			path:          "/v1/bundles/" + fixtureBundleID,
			body:          `{"name":"actor test patched bundle"}`,
			subject:       "curator",
			entityID:      fixtureBundleID,
			expectedCode:  200,
			expectedActor: "curator",
		},
		{
			description:   "DELETE bundle",
			method:        http.MethodDelete,
			path:          "/v1/bundles/" + fixtureBundleID,
			subject:       "curator",
			entityID:      fixtureBundleID,
			expectedCode:  204,
			expectedActor: "curator",
		},
		{
			description:   "POST webhook",
			method:        http.MethodPost,
			path:          "/v1/software/webhooks",
			body:          `{"url":"https://actor-test.example.org/hook"}`,
			subject:       "operator",
			expectedCode:  200,
			expectedActor: "operator",
		},
		{
			description:   "DELETE webhook",
			method:        http.MethodDelete,
			path:          "/v1/webhooks/24bc1b5d-fe81-47be-9d55-910f820bdd04",
			subject:       "operator",
			entityID:      "24bc1b5d-fe81-47be-9d55-910f820bdd04",
			expectedCode:  204,
			expectedActor: "operator",
		},
		{
			description:   "PATCH software analysis",
			method:        http.MethodPatch,
			path:          softwarePath + swissSoftwareID + "/analysis",
			body:          `{"actor-test":{"v":1}}`,
			subject:       "scanner",
			entityID:      swissSoftwareID,
			expectedCode:  200,
			expectedActor: "scanner",
		},
		{
			description:   "PATCH software",
			method:        http.MethodPatch,
			path:          softwarePath + swissSoftwareID,
			body:          `{"vitality":"10,10,10"}`,
			subject:       "editor",
			entityID:      swissSoftwareID,
			expectedCode:  200,
			expectedActor: "editor",
		},
		{
			description:   "DELETE publisher",
			method:        http.MethodDelete,
			path:          publisherPath + deletablePublisherID,
			subject:       "janitor",
			entityID:      deletablePublisherID,
			expectedCode:  204,
			expectedActor: "janitor",
		},
		{
			description:   "DELETE software",
			method:        http.MethodDelete,
			path:          softwarePath + deletableSoftwareID,
			subject:       "janitor",
			entityID:      deletableSoftwareID,
			expectedCode:  204,
			expectedActor: "janitor",
		},
		{
			description:   "POST publisher with a token carrying no subject",
			method:        http.MethodPost,
			path:          "/v1/publishers",
			body:          `{"description":"actor test anonymous publisher","codeHosting":[{"url":"https://actor-test.example.org/anonymous"}]}`,
			subject:       "",
			expectedCode:  200,
			expectedActor: "",
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			loadFixtures(t)

			req, err := newTestRequest(test.method, test.path, strings.NewReader(test.body))
			require.NoError(t, err)

			req.Header = map[string][]string{
				"Authorization": {bearerWithSubject(t, test.subject)},
				"Content-Type":  {"application/json"},
			}

			res, err := app.Test(req, -1)
			require.NoError(t, err)
			require.Equal(t, test.expectedCode, res.StatusCode)

			entityID := test.entityID
			if entityID == "" {
				entityID = idFromResponse(t, res.Body)
			}

			require.Equal(t, 1, dbCount(t, "events", "entity_id", entityID))
			assert.Equal(t, test.expectedActor, dbValue(t, "events", "actor", "entity_id", entityID))
		})
	}
}

// idFromResponse reads the id of the entity a POST created.
func idFromResponse(t *testing.T, body io.Reader) string {
	t.Helper()

	raw, err := io.ReadAll(body)
	require.NoError(t, err)

	id, ok := decodeJSON(t, raw)["id"].(string)
	require.True(t, ok, "no id in the response: %s", raw)

	return id
}
