package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSearchIsIgnoredOutsideLogs covers the lists that never supported
// ?search: only the logs table has a message column to search in, so
// the parameter must leave the other lists untouched instead of
// reaching the database.
func TestSearchIsIgnoredOutsideLogs(t *testing.T) {
	authHeaders := map[string][]string{"Authorization": {goodToken}}

	tests := []TestCase{
		{query: "GET /v1/publishers?search=x", expectedCode: 200, expectedContentType: "application/json"},
		{query: "GET /v1/software?search=x", expectedCode: 200, expectedContentType: "application/json"},
		{query: "GET /v1/catalogs?search=x", expectedCode: 200, expectedContentType: "application/json"},
		{query: "GET /v1/bundles?search=x", expectedCode: 200, expectedContentType: "application/json"},
		{
			query:               "GET /v1/events?search=x",
			headers:             authHeaders,
			expectedCode:        200,
			expectedContentType: "application/json",
		},
		{
			query:               "GET /v1/catalogs/" + italiaID + "/publishers?search=x",
			expectedCode:        200,
			expectedContentType: "application/json",
		},
		{
			query:               "GET /v1/catalogs/" + italiaID + "/software?search=x",
			expectedCode:        200,
			expectedContentType: "application/json",
		},
	}

	for i := range tests {
		tests[i].validateFunc = func(t *testing.T, response map[string]any) {
			assert.NotEmpty(t, assertListResponse(t, response))
		}
	}

	runTestCases(t, tests)
}

// TestLogSearchFoldsNonASCIICase searches log messages with accented
// terms in either case. The term goes through the same LOWER as the
// message, so on a database that folds Unicode letters an uppercase
// accented letter in the term still meets its lowercase form.
func TestLogSearchFoldsNonASCIICase(t *testing.T) {
	loadFixtures(t)

	insert := fmt.Sprintf(
		"INSERT INTO logs (id, message, created_at, updated_at) VALUES (%s, %s, %s, %s)",
		placeholder(1), placeholder(2), placeholder(3), placeholder(4),
	)
	messages := map[string]string{
		"00000000-0000-0000-0000-00000000f001": "Comune di Forlì: folded search",
		"00000000-0000-0000-0000-00000000f002": "ÉCOLE: folded search",
	}

	for id, message := range messages {
		now := time.Now().UTC()
		_, err := db.Exec(insert, id, message, now, now)
		require.NoError(t, err)

		t.Cleanup(func() {
			_, _ = db.Exec("DELETE FROM logs WHERE id = "+placeholder(1), id)
		})
	}

	forli := []string{"Comune di Forlì: folded search"}
	ecole := []string{"ÉCOLE: folded search"}

	tests := []struct {
		search string
		want   []string
	}{
		{search: "forlì", want: forli},
		{search: "Forlì: FOLDED", want: forli},
		{search: "ÉCOLE", want: ecole},
	}

	// SQLite, and PostgreSQL with the C ctype, fold ASCII letters only:
	// there Ì and É keep their case on both sides of the comparison.
	var folded string
	require.NoError(t, db.QueryRow("SELECT LOWER('ÌÉ')").Scan(&folded))

	if folded == "ìé" {
		tests = append(tests, []struct {
			search string
			want   []string
		}{
			{search: "FORLÌ", want: forli},
			{search: "école", want: ecole},
		}...)
	}

	for i, test := range tests {
		t.Run(test.search, func(t *testing.T) {
			path := fmt.Sprintf("/v1/logs?search=%s&foldTest=%d", url.QueryEscape(test.search), i)

			req, err := newTestRequest(http.MethodGet, path, nil)
			require.NoError(t, err)

			res, err := app.Test(req, -1)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, res.Body.Close()) })
			require.Equal(t, http.StatusOK, res.StatusCode)

			var response map[string]any
			require.NoError(t, json.NewDecoder(res.Body).Decode(&response))

			var got []string
			for _, log := range assertListResponse(t, response) {
				got = append(got, log["message"].(string))
			}

			assert.ElementsMatch(t, test.want, got)
		})
	}
}
