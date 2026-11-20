package main

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveRaw sends request to the API over an in-memory connection and
// returns the response with its body read. The request is written in the
// background, so the response can arrive before the server reads the body.
func serveRaw(t *testing.T, request string) (*http.Response, string) {
	t.Helper()

	app.Handler()

	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })

	go func() {
		defer server.Close()

		_ = app.Server().ServeConn(server)
	}()

	require.NoError(t, client.SetDeadline(time.Now().Add(5*time.Second)))

	go func() {
		_, _ = io.WriteString(client, request)
	}()

	response, err := http.ReadResponse(bufio.NewReader(client), nil)
	require.NoError(t, err)

	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	return response, string(body)
}

// readAhead is as much of a body as the server reads before the handlers.
var readAhead = strings.Repeat(" ", 8<<10)

func chunked(body string) string {
	var encoded strings.Builder

	for len(body) > 0 {
		chunk := body[:min(len(body), 64<<10)]
		body = body[len(chunk):]
		fmt.Fprintf(&encoded, "%x\r\n%s\r\n", len(chunk), chunk)
	}

	return encoded.String() + "0\r\n\r\n"
}

func TestRequestHeadersDoNotReserveTheBody(t *testing.T) {
	// Before streaming, the server allocated the declared length and
	// waited for the whole body before the token was checked, so these
	// requests got no answer until the read timeout. Now it reads ahead
	// at most 8 KiB.
	for _, test := range []struct {
		name        string
		contentType string
	}{
		{name: "JSON", contentType: "application/json"},
		{name: "multipart", contentType: "multipart/form-data; boundary=x"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, body := serveRaw(t, "POST /v1/software HTTP/1.1\r\nHost: localhost\r\n"+
				"Content-Type: "+test.contentType+"\r\n"+
				fmt.Sprintf("Content-Length: %d\r\n\r\n", requestBodyLimit)+readAhead)

			assert.Equal(t, http.StatusUnauthorized, response.StatusCode)
			assert.Contains(t, body, `"status":401`)
		})
	}
}

func TestRequestBodyLimit(t *testing.T) {
	tooLarge := fmt.Sprintf(`{"title":"Request Entity Too Large",`+
		`"detail":"request bodies must not be longer than %d bytes","status":413}`, requestBodyLimit)

	// A catalog without a name, padded to the given length, so a body the
	// API reads gets 422 from validation.
	padded := func(length int) string {
		body := `{"name": ""}`

		return body[:len(body)-1] + strings.Repeat(" ", length-len(body)) + "}"
	}

	headers := "POST /v1/catalogs HTTP/1.1\r\nHost: localhost\r\n" +
		"Authorization: " + goodToken + "\r\nContent-Type: application/json\r\n"

	for _, test := range []struct {
		name         string
		request      string
		expectedCode int
		expectedBody string
	}{
		{
			name:         "Content-Length over the limit",
			request:      headers + fmt.Sprintf("Content-Length: %d\r\n\r\n", requestBodyLimit+1) + readAhead,
			expectedCode: http.StatusRequestEntityTooLarge,
			expectedBody: tooLarge,
		},
		{
			name: "Content-Length at the limit",
			request: headers + fmt.Sprintf("Content-Length: %d\r\n\r\n", requestBodyLimit) +
				padded(requestBodyLimit),
			expectedCode: http.StatusUnprocessableEntity,
		},
		{
			name:         "chunked body over the limit",
			request:      headers + "Transfer-Encoding: chunked\r\n\r\n" + chunked(padded(requestBodyLimit+1)),
			expectedCode: http.StatusRequestEntityTooLarge,
			expectedBody: tooLarge,
		},
		{
			name:         "chunked body at the limit",
			request:      headers + "Transfer-Encoding: chunked\r\n\r\n" + chunked(padded(requestBodyLimit)),
			expectedCode: http.StatusUnprocessableEntity,
		},
		{
			name:         "short chunked body",
			request:      headers + "Transfer-Encoding: chunked\r\n\r\n" + chunked(padded(100)),
			expectedCode: http.StatusUnprocessableEntity,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			response, body := serveRaw(t, test.request)

			assert.Equal(t, test.expectedCode, response.StatusCode, body)

			if test.expectedBody != "" {
				assert.JSONEq(t, test.expectedBody, body)
			} else {
				assert.Contains(t, body, `"name"`)
			}
		})
	}
}
