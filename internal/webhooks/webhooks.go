package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

const (
	dialTimeout   = 5 * time.Second
	clientTimeout = 10 * time.Second
)

// errForbiddenAddress is returned when a webhook target is one the dispatcher
// must never reach, so callers can tell it apart from a transport failure.
var errForbiddenAddress = errors.New("webhook target is not publicly routable")

// dispatchTimeout caps the per-request webhook dispatch. It is a var (not
// const) so tests can shorten it without sleeping for whole seconds.
//
//nolint:gochecknoglobals // tunable for tests, effectively const at runtime
var dispatchTimeout = 10 * time.Second

// dialGuardDisabled turns off the SSRF dial guard. Tests that use
// httptest.Server (which binds loopback) set it. An atomic because a
// dispatch goroutine still in flight from an earlier test reads it while
// the next test's cleanup restores it.
//
//nolint:gochecknoglobals // tunable for tests
var dialGuardDisabled atomic.Bool

// isForbiddenIP reports whether ip must not be dialed by the webhook
// dispatcher. IsPrivate covers RFC 1918 and RFC 4193.
func isForbiddenIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsPrivate()
}

// checkHostAllowed refuses a host that is, or resolves to, an address outside
// the publicly routable space.
func checkHostAllowed(ctx context.Context, host string) error {
	if ip := net.ParseIP(host); ip != nil {
		if isForbiddenIP(ip) {
			return fmt.Errorf("%w: %s", errForbiddenAddress, host)
		}

		return nil
	}

	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return fmt.Errorf("resolving webhook host %q: %w", host, err)
	}

	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil && isForbiddenIP(ip) {
			return fmt.Errorf("%w: %s resolves to %s", errForbiddenAddress, host, addr)
		}
	}

	return nil
}

func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}

	if err := checkHostAllowed(ctx, host); err != nil {
		return nil, err
	}

	dialer := &net.Dialer{Timeout: dialTimeout}

	conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
	if err != nil {
		return nil, fmt.Errorf("dialing webhook target %q: %w", addr, err)
	}

	return conn, nil
}

func newWebhookClient() *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if dialGuardDisabled.Load() {
				dialer := &net.Dialer{Timeout: dialTimeout}

				return dialer.DialContext(ctx, network, addr)
			}

			return safeDialContext(ctx, network, addr)
		},
	}

	return &http.Client{
		Timeout:   clientTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if dialGuardDisabled.Load() {
				return nil
			}

			return checkHostAllowed(req.Context(), req.URL.Hostname())
		},
	}
}

// httpClient is shared across dispatches so the underlying http.Transport can
// reuse connections to the same subscriber and so the SSRF dial guard applies
// to every request. The per-request deadline is enforced via the request
// context.
//
//nolint:gochecknoglobals // singleton needed for connection pool reuse
var httpClient = newWebhookClient()

func DispatchWebhooks(event models.Event, gorm *gorm.DB) error {
	var webhooks []models.Webhook

	subject := "/" + event.EntityType
	if event.EntityID != "" {
		subject += "/" + event.EntityID
	}

	// When entity_id == '', the webhook is meant for any event occurred in any
	// resource of that type (fe. Publishers, Software)
	stmt := gorm.
		Where(
			"entity_type = ? AND (entity_id = '' OR entity_id = ?)",
			event.EntityType,
			event.EntityID,
		)

	if err := stmt.Select("url, secret, format").Find(&webhooks).Error; err != nil {
		return fmt.Errorf("error finding webhooks for %s: %w", subject, err)
	}

	for _, webhook := range webhooks {
		body, headers, err := buildRequest(webhook, event, subject)
		if err != nil {
			return fmt.Errorf("error building webhook request for %s: %w", subject, err)
		}

		go post(webhook.URL, body, headers)
	}

	return nil
}

// buildRequest returns the payload and the format-specific headers for one
// webhook. An empty format means the row predates the format column and gets
// the default format.
func buildRequest(webhook models.Webhook, event models.Event, subject string) ([]byte, map[string]string, error) {
	switch webhook.Format {
	case common.WebhookFormatStandardWebhooks:
		return standardWebhooksRequest(webhook, event, subject)
	default:
		return defaultRequest(webhook, event, subject)
	}
}

func defaultRequest(webhook models.Webhook, event models.Event, subject string) ([]byte, map[string]string, error) {
	body, err := json.Marshal(map[string]string{
		"event":   event.Type,
		"subject": subject,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling event JSON for %s: %w", subject, err)
	}

	headers := map[string]string{}

	if webhook.Secret != "" {
		headers["X-Webhook-Signature"] = hex.EncodeToString(hmacSHA256(webhook.Secret, body))
	}

	return body, headers, nil
}

func hmacSHA256(secret string, chunks ...[]byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))

	for _, chunk := range chunks {
		// This can't fail
		_, _ = mac.Write(chunk)
	}

	return mac.Sum(nil)
}

// standardWebhooksRequest builds the payload and headers per
// https://www.standardwebhooks.com: the signed content is
// "<id>.<timestamp>.<body>" and the signature is prefixed with its scheme
// version.
func standardWebhooksRequest(
	webhook models.Webhook, event models.Event, subject string,
) ([]byte, map[string]string, error) {
	now := time.Now()

	body, err := json.Marshal(map[string]any{
		"type":      event.EntityType + "." + event.Type,
		"timestamp": now.UTC().Format(time.RFC3339),
		"data": map[string]string{
			"event":   event.Type,
			"subject": subject,
		},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling event JSON for %s: %w", subject, err)
	}

	timestamp := strconv.FormatInt(now.Unix(), 10)

	headers := map[string]string{
		"webhook-id":        event.ID,
		"webhook-timestamp": timestamp,
	}

	if webhook.Secret != "" {
		signature := hmacSHA256(webhook.Secret, []byte(event.ID+"."+timestamp+"."), body)

		headers["webhook-signature"] = "v1," + base64.StdEncoding.EncodeToString(signature)
	}

	return body, headers, nil
}

func post(url string, body []byte, headers map[string]string) {
	ctx, cancel := context.WithTimeout(context.Background(), dispatchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		url,
		bytes.NewReader(body),
	)
	if err != nil {
		return
	}

	req.Header.Set("User-Agent", "DevelopersItaliaAPI-Webhook/1.0")
	req.Header.Set("Content-Type", "application/json")

	for name, value := range headers {
		req.Header.Set(name, value)
	}

	response, err := httpClient.Do(req)
	if err != nil {
		//nolint:godox // need to implement this in the future
		// TODO: Replace this and send anonymous failure metrics to a monitoring
		// system instead.
		// (https://github.com/publiccodeyml/open-catalog-api/issues/73)
		log.Printf("error while dispatching webhook %s: %s", url, err.Error())

		return
	}

	// Drain and close so the connection can return to the pool, regardless
	// of whether the response is 2xx or an error status below.
	defer func() {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()

	if response.StatusCode < 200 || response.StatusCode > 299 {
		//nolint:godox // need to implement this in the future
		// TODO: Replace this and send anonymous failure metrics to a monitoring
		// system instead.
		// (https://github.com/publiccodeyml/open-catalog-api/issues/73)
		log.Printf("error while dispatching webhook %s: got HTTP %d", url, response.StatusCode)

		return
	}
}
