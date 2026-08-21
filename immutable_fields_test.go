package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	unusedID      = "00000000-dead-beef-0000-000000000001"
	forgedCreated = "1999-01-01T00:00:00Z"

	softwarePath        = "/v1/software/"
	publisherPath       = "/v1/publishers/"
	catalogPath         = "/v1/catalogs/"
	catalogSoftwarePath = catalogPath + italiaID + "/software/"
	catalogPubPath      = catalogPath + italiaID + "/publishers/"
)

// patchJSON sends a RFC 6902 patch, the content type that skips the DTO
// validation and reaches the entity fields directly.
func patchJSON(t *testing.T, path, body string) (int, []byte) {
	t.Helper()

	req, err := newTestRequest(http.MethodPatch, path, strings.NewReader(body))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json-patch+json")
	req.Header.Set("Authorization", goodToken)

	res, err := app.Test(req, -1)
	require.NoError(t, err)

	out, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	return res.StatusCode, out
}

func problem(title, detail string) string {
	return `{"title":"` + title + `","detail":"` + detail + `","status":422}`
}

// TestPatchRejectsProtectedPaths covers every endpoint that runs an incoming
// patch over the whole entity. An operation on a field the API never lets a
// client choose is refused as a whole: the second, harmless operation in each
// patch must not reach the database either.
func TestPatchRejectsProtectedPaths(t *testing.T) {
	const detail = "operation not allowed on a read-only field: "

	tests := []struct {
		description string
		prefix      string
		id          string
		ops         string
		title       string
		table       string
		column      string
		want        string
	}{
		{
			"software id", softwarePath, italiaSoftwareID,
			`{"op": "replace", "path": "/id", "value": "` + unusedID + `"},
			 {"op": "replace", "path": "/publiccodeYml", "value": "patched"}`,
			"can't update Software", "software", "publiccode_yml", "-",
		},
		{
			"software createdAt", softwarePath, italiaSoftwareID,
			`{"op": "replace", "path": "/createdAt", "value": "` + forgedCreated + `"},
			 {"op": "replace", "path": "/publiccodeYml", "value": "patched"}`,
			"can't update Software", "software", "publiccode_yml", "-",
		},
		{
			"software updatedAt", softwarePath, italiaSoftwareID,
			`{"op": "replace", "path": "/updatedAt", "value": "` + forgedCreated + `"},
			 {"op": "replace", "path": "/publiccodeYml", "value": "patched"}`,
			"can't update Software", "software", "publiccode_yml", "-",
		},
		{
			"software catalogId", softwarePath, italiaSoftwareID,
			`{"op": "replace", "path": "/catalogId", "value": "` + swissID + `"},
			 {"op": "replace", "path": "/publiccodeYml", "value": "patched"}`,
			"can't update Software", "software", "publiccode_yml", "-",
		},
		{
			"software add id", softwarePath, italiaSoftwareID,
			`{"op": "add", "path": "/id", "value": "` + unusedID + `"},
			 {"op": "replace", "path": "/publiccodeYml", "value": "patched"}`,
			"can't update Software", "software", "publiccode_yml", "-",
		},
		{
			"software remove createdAt", softwarePath, italiaSoftwareID,
			`{"op": "remove", "path": "/createdAt"},
			 {"op": "replace", "path": "/publiccodeYml", "value": "patched"}`,
			"can't update Software", "software", "publiccode_yml", "-",
		},
		{
			"software move from createdAt", softwarePath, italiaSoftwareID,
			`{"op": "move", "from": "/createdAt", "path": "/publiccodeYml"}`,
			"can't update Software", "software", "publiccode_yml", "-",
		},
		{
			"software copy from id", softwarePath, italiaSoftwareID,
			`{"op": "copy", "from": "/id", "path": "/publiccodeYml"}`,
			"can't update Software", "software", "publiccode_yml", "-",
		},
		{
			"catalog software id", catalogSoftwarePath, italiaSoftwareID,
			`{"op": "replace", "path": "/id", "value": "` + unusedID + `"},
			 {"op": "replace", "path": "/publiccodeYml", "value": "patched"}`,
			"can't update Software", "software", "publiccode_yml", "-",
		},
		{
			"catalog software createdAt", catalogSoftwarePath, italiaSoftwareID,
			`{"op": "replace", "path": "/createdAt", "value": "` + forgedCreated + `"},
			 {"op": "replace", "path": "/publiccodeYml", "value": "patched"}`,
			"can't update Software", "software", "publiccode_yml", "-",
		},
		{
			"catalog software catalogId", catalogSoftwarePath, italiaSoftwareID,
			`{"op": "replace", "path": "/catalogId", "value": "` + swissID + `"},
			 {"op": "replace", "path": "/publiccodeYml", "value": "patched"}`,
			"can't update Software", "software", "publiccode_yml", "-",
		},
		{
			"publisher id", publisherPath, italiaPublisherID,
			`{"op": "replace", "path": "/id", "value": "` + unusedID + `"},
			 {"op": "replace", "path": "/description", "value": "patched"}`,
			"can't update Publisher", "publishers", "description", "Publisher description 1",
		},
		{
			"publisher createdAt", publisherPath, italiaPublisherID,
			`{"op": "replace", "path": "/createdAt", "value": "` + forgedCreated + `"},
			 {"op": "replace", "path": "/description", "value": "patched"}`,
			"can't update Publisher", "publishers", "description", "Publisher description 1",
		},
		{
			"publisher updatedAt", publisherPath, italiaPublisherID,
			`{"op": "replace", "path": "/updatedAt", "value": "` + forgedCreated + `"},
			 {"op": "replace", "path": "/description", "value": "patched"}`,
			"can't update Publisher", "publishers", "description", "Publisher description 1",
		},
		{
			"publisher catalogId", publisherPath, italiaPublisherID,
			`{"op": "replace", "path": "/catalogId", "value": "` + swissID + `"},
			 {"op": "replace", "path": "/description", "value": "patched"}`,
			"can't update Publisher", "publishers", "description", "Publisher description 1",
		},
		{
			"catalog publisher id", catalogPubPath, italiaPublisherID,
			`{"op": "replace", "path": "/id", "value": "` + unusedID + `"},
			 {"op": "replace", "path": "/description", "value": "patched"}`,
			"can't update Publisher", "publishers", "description", "Publisher description 1",
		},
		{
			"catalog publisher createdAt", catalogPubPath, italiaPublisherID,
			`{"op": "replace", "path": "/createdAt", "value": "` + forgedCreated + `"},
			 {"op": "replace", "path": "/description", "value": "patched"}`,
			"can't update Publisher", "publishers", "description", "Publisher description 1",
		},
		{
			"catalog publisher catalogId", catalogPubPath, italiaPublisherID,
			`{"op": "replace", "path": "/catalogId", "value": "` + swissID + `"},
			 {"op": "replace", "path": "/description", "value": "patched"}`,
			"can't update Publisher", "publishers", "description", "Publisher description 1",
		},
		{
			"catalog id", catalogPath, italiaID,
			`{"op": "replace", "path": "/id", "value": "` + unusedID + `"},
			 {"op": "replace", "path": "/name", "value": "patched"}`,
			"can't update Catalog", "catalogs", "name", "Italian Catalog",
		},
		{
			"catalog createdAt", catalogPath, italiaID,
			`{"op": "replace", "path": "/createdAt", "value": "` + forgedCreated + `"},
			 {"op": "replace", "path": "/name", "value": "patched"}`,
			"can't update Catalog", "catalogs", "name", "Italian Catalog",
		},
		{
			"catalog updatedAt", catalogPath, italiaID,
			`{"op": "replace", "path": "/updatedAt", "value": "` + forgedCreated + `"},
			 {"op": "replace", "path": "/name", "value": "patched"}`,
			"can't update Catalog", "catalogs", "name", "Italian Catalog",
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			loadFixtures(t)

			code, body := patchJSON(t, test.prefix+test.id, "["+test.ops+"]")

			assert.Equal(t, http.StatusUnprocessableEntity, code)
			assert.Contains(t, string(body), `"detail":"`+detail)

			assert.Equal(t, test.want, dbValue(t, test.table, test.column, "id", test.id))
			assert.Equal(t, 0, dbCount(t, test.table, "id", unusedID))
		})
	}
}

// TestPatchTestOpOnProtectedPathIsAllowed pins that a read-only field can
// still be named in a test operation, which reads it and changes nothing.
func TestPatchTestOpOnProtectedPathIsAllowed(t *testing.T) {
	tests := []struct {
		description string
		prefix      string
		id          string
		ops         string
		table       string
		column      string
		want        string
	}{
		{
			"software", softwarePath, italiaSoftwareID,
			`{"op": "test", "path": "/id", "value": "` + italiaSoftwareID + `"},
			 {"op": "replace", "path": "/publiccodeYml", "value": "patched"}`,
			"software", "publiccode_yml", "patched",
		},
		{
			"publisher", publisherPath, italiaPublisherID,
			`{"op": "test", "path": "/catalogId", "value": "` + italiaID + `"},
			 {"op": "replace", "path": "/description", "value": "patched"}`,
			"publishers", "description", "patched",
		},
		{
			"catalog", catalogPath, italiaID,
			`{"op": "test", "path": "/id", "value": "` + italiaID + `"},
			 {"op": "replace", "path": "/name", "value": "patched"}`,
			"catalogs", "name", "patched",
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			loadFixtures(t)

			code, body := patchJSON(t, test.prefix+test.id, "["+test.ops+"]")

			assert.Equal(t, http.StatusOK, code, "body: %s", body)

			var response map[string]interface{}
			require.NoError(t, json.Unmarshal(body, &response))
			assert.Equal(t, test.id, response["id"])

			assert.Equal(t, test.want, dbValue(t, test.table, test.column, "id", test.id))
		})
	}
}

// TestPatchWithAFailingTestOpWritesNothing pins the atomicity of the patch
// past the protected path check: a test that fails refuses the whole patch.
func TestPatchWithAFailingTestOpWritesNothing(t *testing.T) {
	loadFixtures(t)

	ops := `[
		{"op": "replace", "path": "/publiccodeYml", "value": "patched"},
		{"op": "test", "path": "/id", "value": "` + unusedID + `"}
	]`

	code, _ := patchJSON(t, softwarePath+italiaSoftwareID, ops)

	assert.Equal(t, http.StatusUnprocessableEntity, code)

	assert.Equal(t, "-", dbValue(t, "software", "publiccode_yml", "id", italiaSoftwareID))
}

// TestPatchRejectsImmutableFieldsInMergePatch covers the other content type,
// where the request is decoded into a patch struct that has no such field.
func TestPatchRejectsImmutableFieldsInMergePatch(t *testing.T) {
	tests := []TestCase{
		{
			description:         "PATCH software with an id",
			query:               "PATCH /v1/software/" + italiaSoftwareID,
			body:                `{"id": "` + unusedID + `"}`,
			headers:             patchHeaders(),
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Software","detail":"unknown field in JSON input","status":422}`,
		},
		{
			description:         "PATCH software with a createdAt",
			query:               "PATCH /v1/software/" + italiaSoftwareID,
			body:                `{"createdAt": "` + forgedCreated + `"}`,
			headers:             patchHeaders(),
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Software","detail":"unknown field in JSON input","status":422}`,
		},
		{
			description:         "PATCH publisher with an id",
			query:               "PATCH /v1/publishers/" + italiaPublisherID,
			body:                `{"id": "` + unusedID + `"}`,
			headers:             patchHeaders(),
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Publisher","detail":"unknown field in JSON input","status":422}`,
		},
		{
			description:         "PATCH catalog with an id",
			query:               "PATCH /v1/catalogs/" + italiaID,
			body:                `{"id": "` + unusedID + `"}`,
			headers:             patchHeaders(),
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Catalog","detail":"unknown field in JSON input","status":422}`,
		},
		{
			description:         "PATCH log with an id",
			query:               "PATCH /v1/logs/2dfb2bc2-042d-11ed-9338-d8bbc146d165",
			body:                `{"id": "` + unusedID + `", "message": "patched"}`,
			headers:             patchHeaders(),
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Log","detail":"unknown field in JSON input","status":422}`,
		},
	}

	runTestCases(t, tests)
}

func patchHeaders() map[string][]string {
	return map[string][]string{
		"Authorization": {goodToken},
		"Content-Type":  {"application/json"},
	}
}
