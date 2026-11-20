package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A JSON Patch test whose value is an array holding null fails like any
// other failing test. The library comparing the arrays used to panic on the
// null item and the request ended with a 500. It compares items only when
// the lengths match, so each value has as many items as the fixture array.
func TestJSONPatchTestWithNullArrayItems(t *testing.T) {
	tests := []struct {
		name  string
		path  string
		field string
		value string
		title string
	}{
		{
			"software aliases", "/v1/software/" + rootSoftwareID,
			"/aliases", `[null]`, "can't update Software",
		},
		{
			"publisher codeHosting", "/v1/publishers/" + rootPublisherID,
			"/codeHosting", `[null, null]`, "can't update Publisher",
		},
		{
			"catalog publisher codeHosting", "/v1/catalogs/" + swissID + "/publishers/" + swissPublisherID,
			"/codeHosting", `[null, null]`, "can't update Publisher",
		},
		{
			"catalog sources", "/v1/catalogs/" + swissID,
			"/sources", `[null]`, "can't update Catalog",
		},
		{
			"bundle softwareIds", "/v1/bundles/" + municipalitiesID,
			"/softwareIds", `[null, null]`, "can't update Bundle",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadFixtures(t)

			code, body := patchJSON(t, tt.path, `[{"op": "test", "path": "`+tt.field+`", "value": `+tt.value+`}]`)
			require.Equal(t, 422, code, "body: %s", body)

			response := decodeJSON(t, body)
			assert.Equal(t, tt.title, response["title"])
			assert.Equal(t, "patch could not be applied", response["detail"])
		})
	}
}
