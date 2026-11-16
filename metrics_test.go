package main

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMetricsServiceLabel(t *testing.T) {
	// The request counters only appear once a request has been served.
	req, err := newTestRequest("GET", "/v1/status", nil)
	require.NoError(t, err)

	res, err := app.Test(req, -1)
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())

	req, err = newTestRequest("GET", "/metrics", nil)
	require.NoError(t, err)

	res, err = app.Test(req, -1)
	require.NoError(t, err)

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	assert.Equal(t, 200, res.StatusCode)
	assert.Contains(t, string(body), `http_requests_total{method="GET",path="/v1/status",service="open-catalog-api",status_code="204"}`)
}

func TestMetricsWebhookDeliveryFailures(t *testing.T) {
	req, err := newTestRequest("GET", "/metrics", nil)
	require.NoError(t, err)

	res, err := app.Test(req, -1)
	require.NoError(t, err)

	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	assert.Equal(t, 200, res.StatusCode)
	assert.Contains(t, string(body), `webhook_delivery_failures_total{reason="transport",service="open-catalog-api"}`)
	assert.Contains(t, string(body), `webhook_delivery_failures_total{reason="status",service="open-catalog-api"}`)
}
