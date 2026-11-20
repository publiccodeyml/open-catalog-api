package main

import (
	"testing"
)

func TestInvalidUTF8InURLIsRefused(t *testing.T) {
	queryProblem := `{"title":"Bad Request","detail":"query parameters must be valid UTF-8","status":400}`
	pathProblem := `{"title":"Not Found","detail":"the path is not valid UTF-8","status":404}`

	tokenHeaders := func() map[string][]string {
		return map[string][]string{"Authorization": {goodToken}}
	}

	tests := []TestCase{
		{query: "GET /v1/logs?search=%ff", expectedCode: 400, expectedBody: queryProblem},
		{query: "GET /v1/logs?filter=%ff", expectedCode: 400, expectedBody: queryProblem},
		{query: "GET /v1/software?url=%ff", expectedCode: 400, expectedBody: queryProblem},
		{query: "GET /v1/software?invalidUTF8Name%ff=1", expectedCode: 400, expectedBody: queryProblem},
		{query: "GET /v1/events?entityId=%ff", headers: tokenHeaders(), expectedCode: 400, expectedBody: queryProblem},
		{query: "GET /v1/events?actor=%c3", headers: tokenHeaders(), expectedCode: 400, expectedBody: queryProblem},
		{query: "GET /v1/events?actor=%ff", expectedCode: 400, expectedBody: queryProblem},
		{query: "GET /v1/publishers/%ff", expectedCode: 404, expectedBody: pathProblem},
		{query: "GET /v1/software/%ff", expectedCode: 404, expectedBody: pathProblem},
		{query: "GET /v1/catalogs/%ff", expectedCode: 404, expectedBody: pathProblem},
		{query: "GET /v1/catalogs/%ff/analysis", expectedCode: 404, expectedBody: pathProblem},
		{query: "GET /v1/catalogs/%ff/software", expectedCode: 404, expectedBody: pathProblem},
		{query: "GET /v1/catalogs/%ff/publishers", expectedCode: 404, expectedBody: pathProblem},
		{query: "DELETE /v1/publishers/%ff", expectedCode: 404, expectedBody: pathProblem},
		{
			description:         "valid UTF-8 in a query value",
			query:               "GET /v1/logs?search=caf%c3%a9&invalidUTF8Control=1",
			expectedCode:        200,
			expectedContentType: "application/json",
			expectedBody:        `{"data":[],"links":{"prev":null,"next":null}}`,
		},
		{
			description:  "valid UTF-8 in the path",
			query:        "GET /v1/publishers/caf%c3%a9?invalidUTF8Control=1",
			expectedCode: 404,
			expectedBody: `{"title":"can't get Publisher","detail":"Publisher was not found","status":404}`,
		},
	}

	for i := range tests {
		if tests[i].expectedContentType == "" {
			tests[i].expectedContentType = "application/problem+json"
		}
	}

	runTestCases(t, tests)
}
