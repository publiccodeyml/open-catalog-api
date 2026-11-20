package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func TestCompressedRequestBodiesAreRefused(t *testing.T) {
	// Decoded, the body is past the 4 MB body limit; encoded, it is a few KB.
	expanding := []byte(`{"name": "Expanded` + strings.Repeat(" ", 5<<20) +
		`", "sources": [{"url": "https://github.com/example/expanded"}]}`)
	gzipped := string(fasthttp.AppendGzipBytes(nil, expanding))
	require.Less(t, len(gzipped), 64<<10)

	plain := []byte(`{"description": "encoded"}`)
	problem := `{"title":"Unsupported Media Type",` +
		`"detail":"request bodies must not be compressed: send them without a Content-Encoding","status":415}`

	headers := func(encoding ...string) map[string][]string {
		return map[string][]string{
			"Authorization":    {goodToken},
			"Content-Type":     {"application/json"},
			"Content-Encoding": encoding,
		}
	}

	tests := []TestCase{
		{
			description:         "POST catalog with a gzip body that expands past the limit",
			query:               "POST /v1/catalogs",
			body:                gzipped,
			headers:             headers("gzip"),
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        problem,
		},
		{
			description:         "PATCH publisher with a gzip body",
			query:               "PATCH /v1/publishers/" + rootPublisherID,
			body:                string(fasthttp.AppendGzipBytes(nil, plain)),
			headers:             headers("gzip"),
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        problem,
		},
		{
			description:         "PATCH publisher with a deflate body",
			query:               "PATCH /v1/publishers/" + rootPublisherID,
			body:                string(fasthttp.AppendDeflateBytes(nil, plain)),
			headers:             headers("deflate"),
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        problem,
		},
		{
			description:         "PATCH publisher with a brotli body",
			query:               "PATCH /v1/publishers/" + rootPublisherID,
			body:                string(fasthttp.AppendBrotliBytes(nil, plain)),
			headers:             headers("br"),
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        problem,
		},
		{
			description: "PATCH publisher with a body gzipped twice",
			query:       "PATCH /v1/publishers/" + rootPublisherID,
			body: string(fasthttp.AppendGzipBytes(nil,
				fasthttp.AppendGzipBytes(nil, plain))),
			headers:             headers("gzip, gzip"),
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        problem,
		},
		{
			description:         "PATCH publisher with a coding behind identity",
			query:               "PATCH /v1/publishers/" + rootPublisherID,
			body:                string(fasthttp.AppendGzipBytes(nil, plain)),
			headers:             headers("identity", "GZIP"),
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        problem,
		},
		{
			description:         "PATCH publisher with an unknown coding",
			query:               "PATCH /v1/publishers/" + rootPublisherID,
			body:                string(plain),
			headers:             headers("x-custom"),
			expectedCode:        415,
			expectedContentType: "application/problem+json",
			expectedBody:        problem,
		},
		{
			description: "POST catalog with a gzip body and no token",
			query:       "POST /v1/catalogs",
			body:        gzipped,
			headers: map[string][]string{
				"Content-Type":     {"application/json"},
				"Content-Encoding": {"gzip"},
			},
			expectedCode:        401,
			expectedContentType: "application/problem+json",
			expectedBody: `{"title":"token authentication failed",` +
				`"detail":"a valid PASETO token is required in the Authorization header","status":401}`,
		},
		{
			description:         "PATCH publisher with the identity coding",
			query:               "PATCH /v1/publishers/" + rootPublisherID,
			body:                string(plain),
			headers:             headers("Identity, identity"),
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, "encoded", response["description"])
			},
		},
		{
			description:         "GET with a Content-Encoding and no body",
			query:               "GET /v1/publishers/" + rootPublisherID + "?encodedNoBody=1",
			headers:             map[string][]string{"Content-Encoding": {"gzip"}},
			expectedCode:        200,
			expectedContentType: "application/json",
			validateFunc: func(t *testing.T, response map[string]any) {
				assert.Equal(t, rootPublisherID, response["id"])
			},
		},
	}

	runTestCases(t, tests)
}

func TestCompressedRequestBodyRefusalNamesIdentity(t *testing.T) {
	req, err := newTestRequest("PATCH", "/v1/publishers/"+rootPublisherID,
		strings.NewReader(string(fasthttp.AppendGzipBytes(nil, []byte(`{}`)))))
	require.NoError(t, err)

	req.Header.Set("Authorization", goodToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")

	res, err := app.Test(req, -1)
	require.NoError(t, err)

	defer res.Body.Close()

	assert.Equal(t, 415, res.StatusCode)
	assert.Equal(t, "identity", res.Header.Get("Accept-Encoding"))
}
