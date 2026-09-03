package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventsEndpoints(t *testing.T) {
	const (
		eventWithActorID    = "0ab7b216-d819-4a2a-8258-65c7dbe3af4d"
		eventWithoutActorID = "d37d1082-528e-449d-a626-445561368d6b"
		eventCount          = 8
	)

	authHeaders := map[string][]string{"Authorization": {goodToken}}

	// The actor of every fixture event, empty for the rows the fixtures
	// leave without one.
	actors := map[string]string{
		"d5e6f708-91a2-4bde-8f30-6b7c8d9e0f75": "crawler",
		"c4d5e6f7-8091-4cad-9e2f-5a6b7c8d9e74": "",
		"b3c4d5e6-7f80-4b9c-8d1e-4f5a6b7c8d73": "crawler",
		"a2b3c4d5-6e7f-4a8b-9c0d-3e4f5a6b7c72": "editor",
		eventWithActorID:                       "crawler",
		eventWithoutActorID:                    "",
		"9b1c2d34-4e5f-4a6b-8c7d-2e3f4a5b6c71": "editor",
		"8e0a1f56-3a70-4b6e-9a2d-1f3b4c5d6e70": "crawler",
	}

	tests := []TestCase{
		// GET /events
		{
			description:         "GET events without a token",
			query:               "GET /v1/events",
			expectedCode:        401,
			expectedBody:        `{"title":"token authentication failed","status":401}`,
			expectedContentType: "application/problem+json",
		},
		{
			description:         "GET events",
			query:               "GET /v1/events",
			headers:             authHeaders,
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				data := assertListResponse(t, response)

				assert.Equal(t, eventCount, len(data))

				// The whole fixture fits into the default page of 25.
				assertPaginationLinks(t, response, nil, nil)

				var prevCreatedAt *time.Time

				for _, event := range data {
					assertUUID(t, event["id"])

					assert.Contains(t, []any{"create", "update", "delete"}, event["type"])
					assert.Contains(t, []any{"software", "publishers"}, event["entityType"])
					assertUUID(t, event["entityId"])

					createdAt := assertRFC3339(t, event["createdAt"])
					assertRFC3339(t, event["updatedAt"])

					id, ok := event["id"].(string)
					require.True(t, ok)

					expectedActor, known := actors[id]
					require.True(t, known, "unexpected event %q in the response", id)
					assertActor(t, event, expectedActor)

					assertOnlyKeys(t, event, "id", "type", "entityType", "entityId", "actor", "createdAt", "updatedAt")

					if prevCreatedAt != nil {
						assert.GreaterOrEqual(t, *prevCreatedAt, createdAt)
					}

					prevCreatedAt = &createdAt
				}
			},
		},
		{
			description:         "GET events with page[size] query param",
			query:               "GET /v1/events?page[size]=3",
			headers:             authHeaders,
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				data := assertListResponse(t, response)

				assert.Equal(t, 3, len(data))

				assertPaginationLinks(t, response, nil, "?page[after]=WyIyMDE5LTA5LTE1VDAwOjAwOjAwWiIsImIzYzRkNWU2LTdmODAtNGI5Yy04ZDFlLTRmNWE2YjdjOGQ3MyJd&page[size]=3")
			},
		},
		{
			description:         "GET events with page[after] query param",
			query:               "GET /v1/events?page[after]=WyIyMDE5LTA5LTE1VDAwOjAwOjAwWiIsImIzYzRkNWU2LTdmODAtNGI5Yy04ZDFlLTRmNWE2YjdjOGQ3MyJd",
			headers:             authHeaders,
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				data := assertListResponse(t, response)

				assert.Equal(t, 5, len(data))

				assertPaginationLinks(t, response, "?page[before]=WyIyMDE4LTExLTMwVDAwOjAwOjAwWiIsImEyYjNjNGQ1LTZlN2YtNGE4Yi05YzBkLTNlNGY1YTZiN2M3MiJd", nil)
			},
		},
		{
			description:         `GET events with "from" query param`,
			query:               "GET /v1/events?from=2019-01-01T00:00:00Z",
			headers:             authHeaders,
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				data := assertListResponse(t, response)

				assert.Equal(t, 3, len(data))
			},
		},
		{
			description:         `GET events with invalid "from" query param`,
			query:               "GET /v1/events?from=3",
			headers:             authHeaders,
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, `can't get Events`, response["title"])
				assert.Equal(t, "invalid date time format (RFC 3339 needed)", response["detail"])
			},
		},
		{
			description:         `GET events with "to" query param`,
			query:               "GET /v1/events?to=2019-01-01T00:00:00Z",
			headers:             authHeaders,
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				data := assertListResponse(t, response)

				assert.Equal(t, 5, len(data))
			},
		},
		{
			description:         `GET events with invalid "to" query param`,
			query:               "GET /v1/events?to=3",
			headers:             authHeaders,
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, `can't get Events`, response["title"])
				assert.Equal(t, "invalid date time format (RFC 3339 needed)", response["detail"])
			},
		},
		{
			description:         `GET events with "from" and "to" query params`,
			query:               "GET /v1/events?from=2016-01-01T00:00:00Z&to=2019-01-01T00:00:00Z",
			headers:             authHeaders,
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				data := assertListResponse(t, response)

				assert.Equal(t, 4, len(data))
			},
		},

		// GET /events/:id
		{
			description:         "GET event without a token",
			query:               "GET /v1/events/" + eventWithActorID,
			expectedCode:        401,
			expectedBody:        `{"title":"token authentication failed","status":401}`,
			expectedContentType: "application/problem+json",
		},
		{
			description:         "GET event with an actor",
			query:               "GET /v1/events/" + eventWithActorID,
			headers:             authHeaders,
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, eventWithActorID, response["id"])
				assert.Equal(t, "update", response["type"])
				assert.Equal(t, "software", response["entityType"])
				assert.Equal(t, "c5dec6fa-8a01-4881-9e7d-132770d4214d", response["entityId"])
				assert.Equal(t, "crawler", response["actor"])

				assertTimestamps(t, response)
				assertOnlyKeys(t, response, "id", "type", "entityType", "entityId", "actor", "createdAt", "updatedAt")
			},
		},
		{
			description: "GET event with no actor",
			query:       "GET /v1/events/" + eventWithoutActorID,
			headers:     authHeaders,
			setupFunc: func(t *testing.T) {
				assert.True(t, dbNull(t, "events", "actor", "id", eventWithoutActorID))
			},
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, eventWithoutActorID, response["id"])
				assert.Equal(t, "create", response["type"])

				assertActor(t, response, "")
				assertOnlyKeys(t, response, "id", "type", "entityType", "entityId", "createdAt", "updatedAt")
			},
		},
		{
			description:         "GET non-existent event",
			query:               "GET /v1/events/eea19c82-0449-11ed-bd84-d8bbc146d165",
			headers:             authHeaders,
			expectedCode:        404,
			expectedBody:        `{"title":"can't get Event","detail":"Event was not found","status":404}`,
			expectedContentType: "application/problem+json",
		},
	}

	runTestCases(t, tests)
}

// assertActor checks the actor of an event, which is absent from the
// response when the token that made the write carried no subject.
func assertActor(t *testing.T, event map[string]any, expected string) {
	t.Helper()

	actor, present := event["actor"]

	if expected == "" {
		assert.False(t, present, "expected no actor, got %v", actor)

		return
	}

	assert.Equal(t, expected, actor)
}
