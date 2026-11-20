package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// catalogDeleteRaceRounds is how many times each race runs. Before the
// create locked its catalog, nearly every round left the resource behind
// a deleted catalog.
const catalogDeleteRaceRounds = 10

// sendAuthorized sends method path with body, if any, and returns the
// status code.
func sendAuthorized(method, path, body string) int {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}

	req, err := newTestRequest(method, path, reader)
	if err != nil {
		return 0
	}

	req.Header = map[string][]string{"Authorization": {goodToken}}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := app.Test(req, -1)
	if err != nil {
		return 0
	}

	_ = res.Body.Close()

	return res.StatusCode
}

// createEmptyCatalog creates a catalog with no resources and returns its id.
func createEmptyCatalog(t *testing.T, round int) string {
	t.Helper()

	body := fmt.Sprintf(`{"name": "Delete race %d", "sources": [{"url": "https://example.org/delete-race-source-%d"}]}`,
		round, round)

	req, err := newTestRequest("POST", "/v1/catalogs", strings.NewReader(body))
	require.NoError(t, err)
	req.Header = map[string][]string{
		"Authorization": {goodToken},
		"Content-Type":  {"application/json"},
	}

	res, err := app.Test(req, -1)
	require.NoError(t, err)
	require.Equal(t, 200, res.StatusCode)

	var created map[string]any
	require.NoError(t, json.NewDecoder(res.Body).Decode(&created))

	return created["id"].(string)
}

// liveRows counts the rows of table whose column is value. On a table with
// soft deletes it skips the deleted rows.
func liveRows(t *testing.T, table, column, value string, softDeletes bool) int {
	t.Helper()

	var n int

	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE %s = %s", table, column, placeholder(1))
	if softDeletes {
		query += " AND deleted_at IS NULL"
	}

	require.NoError(t, db.QueryRow(query, value).Scan(&n))

	return n
}

func TestCatalogDeleteDuringCreate(t *testing.T) {
	tests := []struct {
		name string
		// resource is the path under /v1/catalogs/<id>.
		resource string
		body     func(round int) string
		table    string
		column   string
		// blocksDelete is true when the resource makes the delete answer
		// 409. Catalog logs are soft deleted with the catalog instead.
		blocksDelete bool
		softDeletes  bool
	}{
		{
			name:     "software",
			resource: "software",
			body: func(round int) string {
				return fmt.Sprintf(`{"url": "https://example.org/delete-race/%d", "publiccodeYml": "-"}`, round)
			},
			table:        "software",
			column:       "catalog_id",
			blocksDelete: true,
		},
		{
			name:     "publisher",
			resource: "publishers",
			body: func(round int) string {
				return fmt.Sprintf(`{"description": "Delete race %d", `+
					`"codeHosting": [{"url": "https://example.org/delete-race-%d"}]}`, round, round)
			},
			table:        "publishers",
			column:       "catalog_id",
			blocksDelete: true,
		},
		{
			name:     "catalog log",
			resource: "logs",
			body: func(int) string {
				return `{"message": "delete race"}`
			},
			table:       "logs",
			column:      "entity_id",
			softDeletes: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loadFixtures(t)

			for round := range catalogDeleteRaceRounds {
				catalogID := createEmptyCatalog(t, round)

				start := make(chan struct{})
				created := make(chan int, 1)
				deleted := make(chan int, 1)

				go func() {
					<-start
					created <- sendAuthorized("POST", "/v1/catalogs/"+catalogID+"/"+test.resource, test.body(round))
				}()

				go func() {
					<-start
					deleted <- sendAuthorized("DELETE", "/v1/catalogs/"+catalogID, "")
				}()

				close(start)

				createCode, deleteCode := <-created, <-deleted

				switch {
				case createCode == 404:
					assert.Equal(t, 204, deleteCode, "round %d", round)
				case test.blocksDelete:
					assert.Equal(t, []int{200, 409}, []int{createCode, deleteCode}, "round %d", round)
				default:
					assert.Equal(t, []int{200, 204}, []int{createCode, deleteCode}, "round %d", round)
				}

				if deleteCode == 204 {
					assert.Equal(t, 0, liveRows(t, test.table, test.column, catalogID, test.softDeletes),
						"round %d: %s left behind a deleted catalog", round, test.name)
				}
			}
		})
	}
}
