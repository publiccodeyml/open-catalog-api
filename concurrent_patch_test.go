package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// concurrentPatchRounds is how many times each race runs. Before the
// PATCH read the row inside its write transaction, nearly every round lost
// one of the two writes, so a handful is enough to catch a regression.
const concurrentPatchRounds = 10

// patchConcurrently sends the two PATCH bodies to path at the same time
// and returns both status codes, sorted.
func patchConcurrently(t *testing.T, path, contentType string, bodies [2]string) []int {
	t.Helper()

	start := make(chan struct{})
	statuses := make(chan int, len(bodies))

	for _, body := range bodies {
		go func() {
			req, err := newTestRequest("PATCH", path, strings.NewReader(body))
			if err != nil {
				statuses <- 0

				return
			}

			req.Header = map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {contentType},
			}

			<-start

			res, err := app.Test(req, -1)
			if err != nil {
				statuses <- 0

				return
			}

			_ = res.Body.Close()

			statuses <- res.StatusCode
		}()
	}

	close(start)

	codes := []int{<-statuses, <-statuses}
	slices.Sort(codes)

	return codes
}

func TestConcurrentPatchesKeepBothWrites(t *testing.T) {
	const activeColumn = "CASE WHEN active THEN 'true' ELSE 'false' END"

	tests := []struct {
		name   string
		table  string
		id     string
		path   string
		column string
	}{
		{"software", "software", rootSoftwareID, "/v1/software/" + rootSoftwareID, "publiccode_yml"},
		{"publisher", "publishers", rootPublisherID, "/v1/publishers/" + rootPublisherID, "description"},
		{"catalog", "catalogs", swissID, "/v1/catalogs/" + swissID, "name"},
		{"bundle", "bundles", municipalitiesID, "/v1/bundles/" + municipalitiesID, "name"},
	}

	field := map[string]string{
		"publiccode_yml": "publiccodeYml",
		"description":    "description",
		"name":           "name",
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loadFixtures(t)

			for range concurrentPatchRounds {
				_, err := db.Exec(
					"UPDATE "+test.table+" SET active = "+placeholder(1)+", "+test.column+" = "+placeholder(2)+
						" WHERE id = "+placeholder(3),
					true, "before", test.id,
				)
				require.NoError(t, err)

				codes := patchConcurrently(t, test.path, "application/json", [2]string{
					`{"active": false}`,
					`{"` + field[test.column] + `": "after"}`,
				})

				require.Equal(t, []int{200, 200}, codes)
				assert.Equal(t, "false", dbValue(t, test.table, activeColumn, "id", test.id))
				assert.Equal(t, "after", dbValue(t, test.table, test.column, "id", test.id))
			}
		})
	}
}

func TestConcurrentJSONPatchTestOperationGuardsTheWrite(t *testing.T) {
	const logID = "4f95b0d0-042e-11ed-8253-d8bbc146d165"

	loadFixtures(t)

	patch := func(message string) string {
		return `[{"op": "test", "path": "/message", "value": "before"},` +
			`{"op": "replace", "path": "/message", "value": "` + message + `"}]`
	}

	for range concurrentPatchRounds {
		_, err := db.Exec("UPDATE logs SET message = "+placeholder(1)+" WHERE id = "+placeholder(2), "before", logID)
		require.NoError(t, err)

		codes := patchConcurrently(t, "/v1/logs/"+logID, common.ContentTypeJSONPatch,
			[2]string{patch("first"), patch("second")})

		// The second PATCH reads the message the first one wrote, so its
		// test operation fails.
		require.Equal(t, []int{200, 422}, codes)
	}
}
