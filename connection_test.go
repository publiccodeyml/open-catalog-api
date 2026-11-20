package main

import (
	"bufio"
	"bytes"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionTimeouts(t *testing.T) {
	t.Parallel()

	config := app.Config()
	require.Equal(t, 30*time.Second, config.ReadTimeout)
	require.Equal(t, 120*time.Second, config.IdleTimeout)
	require.Equal(t, 5*time.Minute, config.WriteTimeout)

	// Exercise the configured server with short deadlines instead of
	// waiting for the production limits in every test run.
	config.ReadTimeout = 20 * time.Millisecond
	config.IdleTimeout = 20 * time.Millisecond

	for _, test := range []struct {
		name    string
		request string
		idle    bool
	}{
		{
			name:    "incomplete headers",
			request: "GET / HTTP/1.1\r\nHost: localhost\r\n",
		},
		{
			name:    "incomplete body",
			request: "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 10\r\n\r\nx",
		},
		{
			name:    "idle keep-alive",
			request: "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n",
			idle:    true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			serverApp := fiber.New(config)
			serverApp.All("/", func(ctx *fiber.Ctx) error {
				return ctx.SendStatus(http.StatusNoContent)
			})

			serverApp.Handler()

			client, server := net.Pipe()
			t.Cleanup(func() { _ = client.Close() })

			done := make(chan struct{})
			go func() {
				defer close(done)
				defer server.Close()

				_ = serverApp.Server().ServeConn(server)
			}()

			require.NoError(t, client.SetDeadline(time.Now().Add(2*time.Second)))
			_, err := io.WriteString(client, test.request)
			require.NoError(t, err)

			reader := bufio.NewReader(client)
			if test.idle {
				response, err := http.ReadResponse(reader, nil)
				require.NoError(t, err)
				require.Equal(t, http.StatusNoContent, response.StatusCode)
				require.NoError(t, response.Body.Close())
			}

			// A client deadline is an error. EOF means the server closed
			// the connection after its own deadline.
			_, err = io.ReadAll(reader)
			require.NoError(t, err)

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				assert.Fail(t, "server did not release the connection")
			}
		})
	}
}

func TestWriteTimeout(t *testing.T) {
	t.Parallel()

	// A body larger than any socket buffer, so the server write blocks
	// until the client reads it.
	body := bytes.Repeat([]byte("x"), 4<<20)

	config := app.Config()
	config.WriteTimeout = 100 * time.Millisecond

	for _, test := range []struct {
		name string
		read bool
	}{
		{name: "client never reads"},
		{name: "client reads", read: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			serverApp := fiber.New(config)
			serverApp.Get("/", func(ctx *fiber.Ctx) error {
				return ctx.Send(body)
			})

			serverApp.Handler()

			client, server := net.Pipe()
			t.Cleanup(func() { _ = client.Close() })

			done := make(chan struct{})
			go func() {
				defer close(done)
				defer server.Close()

				_ = serverApp.Server().ServeConn(server)
			}()

			require.NoError(t, client.SetDeadline(time.Now().Add(2*time.Second)))
			_, err := io.WriteString(client, "GET / HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n")
			require.NoError(t, err)

			if test.read {
				response, err := http.ReadResponse(bufio.NewReader(client), nil)
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, response.StatusCode)

				received, err := io.ReadAll(response.Body)
				require.NoError(t, err)
				require.Equal(t, len(body), len(received))
				require.NoError(t, response.Body.Close())
			}

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				assert.Fail(t, "server did not release the connection")
			}
		})
	}
}
