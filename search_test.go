package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
