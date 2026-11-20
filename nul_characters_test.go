package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNULCharactersAreRefused(t *testing.T) {
	// Each case needs its own map: the test client adds Content-Length to
	// it, and a shared map would send the length of an earlier body.
	jsonHeaders := func() map[string][]string {
		return map[string][]string{
			"Authorization": {goodToken},
			"Content-Type":  {"application/json"},
		}
	}

	urlProblem := `{"title":"Bad Request","detail":"the URL must not contain NUL characters (%00)","status":400}`

	tests := []TestCase{
		{
			description:         "POST publisher with a NUL in the description",
			query:               "POST /v1/publishers",
			body:                `{"description":"nul\u0000pub","codeHosting":[{"url":"https://example.org/nul"}]}`,
			headers:             jsonHeaders(),
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't create Publisher","detail":"the string at /description must not contain NUL characters (U+0000)","status":422}`,
		},
		{
			description:         "POST log with a NUL in the message",
			query:               "POST /v1/logs",
			body:                `{"message":"m\u0000x"}`,
			headers:             jsonHeaders(),
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't create Log","detail":"the string at /message must not contain NUL characters (U+0000)","status":422}`,
		},
		{
			description:         "POST log with an escaped backslash before u0000",
			query:               "POST /v1/logs",
			body:                `{"message":"m\\u0000x"}`,
			headers:             jsonHeaders(),
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, `m\u0000x`, response["message"])
			},
		},
		{
			description:         "PATCH software with a merge patch carrying a NUL",
			query:               "PATCH /v1/software/" + rootSoftwareID,
			body:                `{"publiccodeYml":"x\u0000y"}`,
			headers:             jsonHeaders(),
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Software","detail":"the string at /publiccodeYml must not contain NUL characters (U+0000)","status":422}`,
		},
		{
			description: "PATCH software with a JSON Patch carrying a NUL",
			query:       "PATCH /v1/software/" + rootSoftwareID,
			body:        `[{"op":"replace","path":"/publiccodeYml","value":"x\u0000y"}]`,
			headers: map[string][]string{
				"Authorization": {goodToken},
				"Content-Type":  {"application/json-patch+json"},
			},
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Software","detail":"the string at /0/value must not contain NUL characters (U+0000)","status":422}`,
		},
		{
			description:         "PATCH software analysis with a NUL in a member name",
			query:               "PATCH /v1/software/" + rootSoftwareID + "/analysis",
			body:                `{"badges":{"v":1,"a\u0000b":true}}`,
			headers:             jsonHeaders(),
			expectedCode:        422,
			expectedContentType: "application/problem+json",
			expectedBody:        `{"title":"can't update Software analysis","detail":"the string at /badges/a\u0000b must not contain NUL characters (U+0000)","status":422}`,
		},
		{
			description:         "GET software with a NUL in a query value, no token",
			query:               "GET /v1/software?url=https://example.org/x%00y",
			expectedCode:        400,
			expectedContentType: "application/problem+json",
			expectedBody:        urlProblem,
		},
		{
			description:         "GET software with a NUL in a query name, no token",
			query:               "GET /v1/software?u%00rl=x",
			expectedCode:        400,
			expectedContentType: "application/problem+json",
			expectedBody:        urlProblem,
		},
		{
			description:         "GET publisher with a NUL in the path, no token",
			query:               "GET /v1/publishers/a%00b",
			expectedCode:        400,
			expectedContentType: "application/problem+json",
			expectedBody:        urlProblem,
		},
	}

	runTestCases(t, tests)
}
