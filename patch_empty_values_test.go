package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A PATCH setting a field to "" gets the 422 a POST gives for the same
// field, and leaves the stored value alone.
func TestPatchRefusesEmptyValues(t *testing.T) {
	tests := []struct {
		name   string
		patch  func(t *testing.T, path, body string) (int, []byte)
		path   string
		body   string
		field  string
		rule   string
		table  string
		column string
		id     string
	}{
		{
			name: "software publiccodeYml", patch: patchMerge,
			path: softwarePath + rootSoftwareID, body: `{"publiccodeYml": ""}`,
			field: "publiccodeYml", rule: "min",
			table: "software", column: "publiccode_yml", id: rootSoftwareID,
		},
		{
			name: "software publiccodeYml via JSON Patch", patch: patchJSON,
			path: softwarePath + rootSoftwareID, body: `[{"op": "replace", "path": "/publiccodeYml", "value": ""}]`,
			field: "publiccodeYml", rule: "min",
			table: "software", column: "publiccode_yml", id: rootSoftwareID,
		},
		{
			name: "catalog alternativeId", patch: patchMerge,
			path: catalogPath + swissID, body: `{"alternativeId": ""}`,
			field: "alternativeId", rule: "min",
			table: "catalogs", column: "COALESCE(alternative_id, 'NULL')", id: swissID,
		},
		{
			name: "publisher description", patch: patchMerge,
			path: publisherPath + rootPublisherID, body: `{"description": ""}`,
			field: "description", rule: "min",
			table: "publishers", column: "description", id: rootPublisherID,
		},
		{
			name: "publisher description via JSON Patch", patch: patchJSON,
			path: publisherPath + rootPublisherID, body: `[{"op": "replace", "path": "/description", "value": ""}]`,
			field: "description", rule: "min",
			table: "publishers", column: "description", id: rootPublisherID,
		},
		{
			name: "publisher alternativeId", patch: patchMerge,
			path: publisherPath + rootPublisherID, body: `{"alternativeId": ""}`,
			field: "alternativeId", rule: "min",
			table: "publishers", column: "COALESCE(alternative_id, 'NULL')", id: rootPublisherID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadFixtures(t)

			before := dbValue(t, tt.table, tt.column, "id", tt.id)

			code, body := tt.patch(t, tt.path, tt.body)
			require.Equal(t, 422, code, "body: %s", body)

			response := decodeJSON(t, body)
			validationErrors, ok := response["validationErrors"].([]any)
			require.True(t, ok, response)
			require.Len(t, validationErrors, 1, response)

			validationError := validationErrors[0].(map[string]any)
			assert.Equal(t, tt.field, validationError["field"])
			assert.Equal(t, tt.rule, validationError["rule"])

			assert.Equal(t, before, dbValue(t, tt.table, tt.column, "id", tt.id))
		})
	}
}

// A row already holding "" from before the fix fails every PATCH with a
// 422 naming the field, until the PATCH sets a value for it.
func TestPatchOnStoredEmptyValues(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		table  string
		column string
		id     string
		field  string
		fixed  string
	}{
		{
			name: "software publiccodeYml", path: softwarePath + rootSoftwareID,
			table: "software", column: "publiccode_yml", id: rootSoftwareID,
			field: "publiccodeYml", fixed: `"publiccodeYmlVersion: \"0.4\"\n"`,
		},
		{
			name: "catalog alternativeId", path: catalogPath + swissID,
			table: "catalogs", column: "alternative_id", id: swissID,
			field: "alternativeId", fixed: `"stored-empty-catalog"`,
		},
		{
			name: "publisher description", path: publisherPath + rootPublisherID,
			table: "publishers", column: "description", id: rootPublisherID,
			field: "description", fixed: `"Stored empty description"`,
		},
		{
			name: "publisher alternativeId", path: publisherPath + rootPublisherID,
			table: "publishers", column: "alternative_id", id: rootPublisherID,
			field: "alternativeId", fixed: `"stored-empty-publisher"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadFixtures(t)

			_, err := db.Exec(
				fmt.Sprintf("UPDATE %s SET %s = '' WHERE id = %s", tt.table, tt.column, placeholder(1)),
				tt.id,
			)
			require.NoError(t, err)

			code, body := patchMerge(t, tt.path, `{"active": false}`)
			require.Equal(t, 422, code, "body: %s", body)

			response := decodeJSON(t, body)
			validationErrors, ok := response["validationErrors"].([]any)
			require.True(t, ok, response)
			require.Len(t, validationErrors, 1, response)

			validationError := validationErrors[0].(map[string]any)
			assert.Equal(t, tt.field, validationError["field"])
			assert.Equal(t, "min", validationError["rule"])

			code, body = patchMerge(t, tt.path, fmt.Sprintf(`{"active": false, %q: %s}`, tt.field, tt.fixed))
			require.Equal(t, 200, code, "body: %s", body)
			assert.NotEmpty(t, dbValue(t, tt.table, tt.column, "id", tt.id))
		})
	}
}
