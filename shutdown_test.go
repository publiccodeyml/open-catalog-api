package main

import (
	"errors"
	"net"
	"net/http"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"github.com/publiccodeyml/open-catalog-api/internal/webhooks"
	"github.com/stretchr/testify/require"
)

func TestShutdownDrainsEventsFromActiveRequests(t *testing.T) { //nolint:funlen
	events := make(chan models.Event, 4)
	dispatched := make(chan models.Event, 4)
	debouncer := webhooks.NewDebouncer(time.Hour, time.Hour, func(event models.Event) { dispatched <- event })
	stopEvents := startEventWorker(events, debouncer)
	app := fiber.New()
	entered := make(chan struct{})
	release := make(chan struct{})
	app.Post("/write", func(ctx fiber.Ctx) error {
		close(entered)
		<-release
		for _, id := range []string{"first", "second", "third"} {
			events <- models.Event{EntityID: id}
		}
		return ctx.SendStatus(http.StatusCreated)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	signals := make(chan os.Signal, 1)
	stopped := make(chan error, 1)
	listenReturned := make(chan struct{})
	go func() {
		stopped <- serveUntilShutdown(app, func() error {
			err := app.Listener(listener)
			close(listenReturned)
			return err
		}, signals, stopEvents)
	}()
	requestDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 5 * time.Second}
		response, err := client.Post("http://"+listener.Addr().String()+"/write", "application/json", nil)
		if err == nil {
			err = response.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not start")
	}
	signals <- syscall.SIGTERM
	// fasthttp closes the listener before waiting for active handlers.
	select {
	case <-listenReturned:
	case <-time.After(5 * time.Second):
		t.Fatal("listener did not stop")
	}
	select {
	case err := <-stopped:
		t.Fatalf("shutdown returned before the handler: %v", err)
	default:
	}
	close(release)
	select {
	case err := <-stopped:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not finish")
	}
	require.NoError(t, <-requestDone)
	require.Len(t, dispatched, 3)
	require.Empty(t, events)
}

func TestListenFailureStopsBackgroundWorker(t *testing.T) {
	expected := errors.New("listen failed")
	stopped := false
	err := serveUntilShutdown(fiber.New(), func() error { return expected }, make(chan os.Signal), func() { stopped = true })
	require.ErrorIs(t, err, expected)
	require.True(t, stopped)
}
