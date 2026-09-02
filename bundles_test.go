package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	municipalitiesID = "e1f2a3b4-c5d6-4e7f-8a9b-0c1d2e3f4a5b"
	schoolsID        = "f2a3b4c5-d6e7-4f8a-9b0c-1d2e3f4a5b6c"
	inactiveID       = "a3b4c5d6-e7f8-4a9b-0c1d-2e3f4a5b6c7d"

	existingSoftwareID  = "c353756e-8597-4e46-a99b-7da2e141603b"
	existingSoftwareID2 = "9f135268-a37e-4ead-96ec-e4a24bb9344a"
	existingSoftwareID3 = "18348f13-1076-4a1e-b204-ed541b824d64"
)

func TestBundleEndpoints(t *testing.T) {
	tests := []TestCase{
		{
			description:         "GET bundles returns only active",
			query:               "GET /v1/bundles",
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]interface{}) {
				data := assertListResponse(t, response)

				assert.Equal(t, 2, len(data))

				first := data[0]
				assertUUID(t, first["id"])
				assertTimestamps(t, first)
				assertOnlyKeys(t, first, "id", "name", "description", "active", "softwareIds", "createdAt", "updatedAt")
			},
		},
		{
			description:         "GET bundles with all=true returns inactive too",
			query:               "GET /v1/bundles?all=true",
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]interface{}) {
				data := assertListResponse(t, response)
				assert.Equal(t, 3, len(data))
			},
		},

		{
			description:         "GET bundle by id",
			query:               "GET /v1/bundles/" + municipalitiesID,
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]interface{}) {
				assert.Equal(t, municipalitiesID, response["id"])
				assert.Equal(t, "Bundle for municipalities", response["name"])
				assert.Equal(t, "Recommended software for municipalities", response["description"])
				assertOnlyKeys(t, response, "id", "name", "description", "active", "softwareIds", "createdAt", "updatedAt")

				ids, ok := response["softwareIds"].([]interface{})
				assert.True(t, ok)
				assert.Equal(t, 2, len(ids))
			},
		},
		{
			description:         "GET bundle not found",
			query:               "GET /v1/bundles/nonexistent",
			expectedCode:        404,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't get Bundle","detail":"Bundle was not found","status":404}`,
		},

		{
			description: "POST bundle",
			query:       "POST /v1/bundles",
			body:        `{"name": "New bundle", "description": "Some description", "softwareIds": ["` + existingSoftwareID + `"]}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]interface{}) {
				assertUUID(t, response["id"])
				assert.Equal(t, "New bundle", response["name"])
				assert.Equal(t, "Some description", response["description"])
				assertOnlyKeys(t, response, "id", "name", "description", "active", "softwareIds", "createdAt", "updatedAt")

				ids, ok := response["softwareIds"].([]interface{})
				assert.True(t, ok)
				assert.Equal(t, 1, len(ids))
			},
		},
		{
			description: "POST bundle missing name",
			query:       "POST /v1/bundles",
			body:        `{"softwareIds": ["` + existingSoftwareID + `"]}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't create Bundle","detail":"invalid format: name is required","status":422,"validationErrors":[{"field":"name","rule":"required","value":""}]}`,
		},
		{
			description: "POST bundle missing softwareIds",
			query:       "POST /v1/bundles",
			body:        `{"name": "Test"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't create Bundle","detail":"invalid format: softwareIds is required","status":422,"validationErrors":[{"field":"softwareIds","rule":"required","value":""}]}`,
		},
		{
			description: "POST bundle with empty softwareIds is rejected",
			query:       "POST /v1/bundles",
			body:        `{"name": "Empty refs", "softwareIds": []}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't create Bundle","detail":"invalid format: softwareIds does not meet its size limits (too few items)","status":422,"validationErrors":[{"field":"softwareIds","rule":"gt","value":""}]}`,
		},
		{
			description: "POST bundle with nonexistent software",
			query:       "POST /v1/bundles",
			body:        `{"name": "Bad refs", "softwareIds": ["00000000-0000-4000-a000-000000000000"]}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't create Bundle","detail":"one or more softwareIds do not exist","status":422}`,
		},
		{
			description: "POST bundle duplicate name",
			query:       "POST /v1/bundles",
			body:        `{"name": "Bundle for municipalities", "softwareIds": ["` + existingSoftwareID + `"]}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        409,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't create Bundle","detail":"name already exists","status":409}`,
		},
		{
			description:         "POST bundle no token",
			query:               "POST /v1/bundles",
			body:                `{"name": "Unauth"}`,
			expectedCode:        401,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"token authentication failed","status":401}`,
		},

		{
			description: "PATCH bundle name",
			query:       "PATCH /v1/bundles/" + municipalitiesID,
			body:        `{"name": "Updated bundle"}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]interface{}) {
				assert.Equal(t, municipalitiesID, response["id"])
				assert.Equal(t, "Updated bundle", response["name"])
			},
		},
		{
			description: "PATCH bundle softwareIds",
			query:       "PATCH /v1/bundles/" + municipalitiesID,
			body:        `{"softwareIds": ["` + existingSoftwareID3 + `"]}`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json"},
			},
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]interface{}) {
				ids, ok := response["softwareIds"].([]interface{})
				assert.True(t, ok)
				assert.Equal(t, 1, len(ids))
				assert.Equal(t, existingSoftwareID3, ids[0])
			},
		},
		{
			description:         "PATCH bundle not found",
			query:               "PATCH /v1/bundles/nonexistent",
			body:                `{"name": "Nope"}`,
			headers:             map[string][]string{"Authorization": {goodToken}, "Content-Type": {"application/json"}},
			expectedCode:        404,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Bundle","detail":"Bundle was not found","status":404}`,
		},
		{
			description:         "PATCH bundle with nonexistent software",
			query:               "PATCH /v1/bundles/" + municipalitiesID,
			body:                `{"softwareIds": ["00000000-0000-4000-a000-000000000000"]}`,
			headers:             map[string][]string{"Authorization": {goodToken}, "Content-Type": {"application/json"}},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Bundle","detail":"one or more softwareIds do not exist","status":422}`,
		},
		{
			description: "PATCH bundle JSON Patch",
			query:       "PATCH /v1/bundles/" + municipalitiesID,
			body:        `[{"op": "replace", "path": "/name", "value": "Patched"}]`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json-patch+json"},
			},
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]interface{}) {
				assert.Equal(t, "Patched", response["name"])
			},
		},

		{
			description: "DELETE bundle",
			query:       "DELETE /v1/bundles/" + municipalitiesID,
			headers: map[string][]string{
				"Authorization": {goodToken},
			},
			expectedCode:        204,
			expectedContentType: "",
		},
		{
			description:         "DELETE bundle not found",
			query:               "DELETE /v1/bundles/nonexistent",
			headers:             map[string][]string{"Authorization": {goodToken}},
			expectedCode:        404,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't delete Bundle","detail":"Bundle was not found","status":404}`,
		},
		{
			description:         "DELETE bundle no token",
			query:               "DELETE /v1/bundles/" + municipalitiesID,
			expectedCode:        401,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"token authentication failed","status":401}`,
		},
	}

	runTestCases(t, tests)
}

func TestBundlePostDBChecks(t *testing.T) {
	t.Run("POST bundle persists to DB", func(t *testing.T) {
		loadFixtures(t)

		body := `{"name": "DB bundle", "softwareIds": ["` + existingSoftwareID + `"]}`
		req, err := newTestRequest(http.MethodPost, "/v1/bundles", strings.NewReader(body))
		require.NoError(t, err)
		req.Header = map[string][]string{
			"Authorization": {goodToken},
			"Content-Type":  {"application/json"},
		}

		res, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, 200, res.StatusCode)

		var response map[string]interface{}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&response))
		bundleID, ok := response["id"].(string)
		require.True(t, ok)

		assert.Equal(t, 1, dbCount(t, "bundles", "name", "DB bundle"))
		assert.Equal(t, "DB bundle", dbValue(t, "bundles", "name", "name", "DB bundle"))
		assert.Equal(t, 1, dbCount(t, "bundles_software", "bundle_id", bundleID))
		assert.Equal(t, existingSoftwareID, dbValue(t, "bundles_software", "software_id", "bundle_id", bundleID))
	})
}

func TestBundlePatchDBChecks(t *testing.T) {
	t.Run("PATCH bundle persists name change to DB", func(t *testing.T) {
		loadFixtures(t)

		const bundleID = municipalitiesID

		body := `{"name": "Updated name"}`
		req, err := newTestRequest(http.MethodPatch, "/v1/bundles/"+bundleID, strings.NewReader(body))
		require.NoError(t, err)
		req.Header = map[string][]string{
			"Authorization": {goodToken},
			"Content-Type":  {"application/json"},
		}

		res, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, 200, res.StatusCode)

		assert.Equal(t, "Updated name", dbValue(t, "bundles", "name", "id", bundleID))
	})

	t.Run("PATCH bundle replaces software links in DB", func(t *testing.T) {
		loadFixtures(t)

		const bundleID = municipalitiesID

		body := `{"softwareIds": ["` + existingSoftwareID3 + `"]}`
		req, err := newTestRequest(http.MethodPatch, "/v1/bundles/"+bundleID, strings.NewReader(body))
		require.NoError(t, err)
		req.Header = map[string][]string{
			"Authorization": {goodToken},
			"Content-Type":  {"application/json"},
		}

		res, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, 200, res.StatusCode)

		assert.Equal(t, 1, dbCount(t, "bundles_software", "bundle_id", bundleID))
		assert.Equal(t, existingSoftwareID3, dbValue(t, "bundles_software", "software_id", "bundle_id", bundleID))
	})
}

func TestBundleDeleteDBChecks(t *testing.T) {
	t.Run("DELETE bundle removes the row and its software links", func(t *testing.T) {
		loadFixtures(t)

		req, err := newTestRequest(http.MethodDelete, "/v1/bundles/"+municipalitiesID, nil)
		require.NoError(t, err)
		req.Header = map[string][]string{
			"Authorization": {goodToken},
		}

		res, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, 204, res.StatusCode)

		assert.Equal(t, 0, dbCount(t, "bundles", "id", municipalitiesID))
		assert.Equal(t, 0, dbCount(t, "bundles_software", "bundle_id", municipalitiesID))
		assert.Equal(t, 1, dbCount(t, "software", "id", existingSoftwareID))
	})

	t.Run("DELETE software removes its bundle links", func(t *testing.T) {
		loadFixtures(t)

		req, err := newTestRequest(http.MethodDelete, "/v1/software/"+existingSoftwareID3, nil)
		require.NoError(t, err)
		req.Header = map[string][]string{
			"Authorization": {goodToken},
		}

		res, err := app.Test(req, -1)
		require.NoError(t, err)
		assert.Equal(t, 204, res.StatusCode)

		assert.Equal(t, 0, dbCount(t, "bundles_software", "software_id", existingSoftwareID3))
		assert.Equal(t, 1, dbCount(t, "bundles", "id", schoolsID))
	})
}
