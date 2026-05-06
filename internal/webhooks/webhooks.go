package webhooks

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

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

// privateRanges are the RFC 1918 / ULA blocks checked at dial time.
//
//nolint:gochecknoglobals // effectively const, computed once
var privateRanges = func() []*net.IPNet {
	cidrs := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"fc00::/7",
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, _ := net.ParseCIDR(cidr)
		nets = append(nets, n)
	}
	return nets
}()

func isForbiddenIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, r := range privateRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
}

func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address %q: %w", addr, err)
	}

	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("DNS lookup failed for %q: %w", host, err)
	}

	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip != nil && isForbiddenIP(ip) {
			return nil, fmt.Errorf("webhook dial to forbidden address %s: %s", a, host)
		}
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
}

func newWebhookClient() *http.Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if dialGuardDisabled.Load() {
				dialer := &net.Dialer{Timeout: 5 * time.Second}
				return dialer.DialContext(ctx, network, addr)
			}
			return safeDialContext(ctx, network, addr)
		},
	}

	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if dialGuardDisabled.Load() {
				return nil
			}

			host := req.URL.Hostname()
			if ip := net.ParseIP(host); ip != nil {
				if isForbiddenIP(ip) {
					return fmt.Errorf("webhook redirect to forbidden address: %s", host)
				}
				return nil
			}

			addrs, err := net.LookupHost(host)
			if err != nil {
				return fmt.Errorf("DNS lookup failed during redirect for %q: %w", host, err)
			}

			for _, a := range addrs {
				if ip := net.ParseIP(a); ip != nil && isForbiddenIP(ip) {
					return fmt.Errorf("webhook redirect to forbidden address %s: %s", a, host)
				}
			}

			return nil
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

	if err := stmt.Select("url, secret").Find(&webhooks).Error; err != nil {
		return fmt.Errorf("error finding webhooks for %s: %w", subject, err)
	}

	jsonBody, err := json.Marshal(map[string]string{
		"event":   event.Type,
		"subject": subject,
	})
	if err != nil {
		return fmt.Errorf("error marshaling event JSON for %s: %w", subject, err)
	}

	for _, webhook := range webhooks {
		signature := ""

		if webhook.Secret != "" {
			h := hmac.New(sha256.New, []byte(webhook.Secret))

			// This can't fail
			_, _ = h.Write(jsonBody)

			signature = hex.EncodeToString(h.Sum(nil))
		}

		go post(webhook.URL, jsonBody, signature)
	}

	return nil
}

func post(url string, body []byte, signature string) {
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

	if signature != "" {
		req.Header.Set("X-Webhook-Signature", signature)
	}

	response, err := httpClient.Do(req)
	if err != nil {
		//nolint:godox // need to implement this in the future
		// TODO: Replace this and send anonymous failure metrics to a monitoring
		// system instead.
		// (https://github.com/italia/developers-italia-api/issues/73)
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
		// (https://github.com/italia/developers-italia-api/issues/73)
		log.Printf("error while dispatching webhook %s: got HTTP %d", url, response.StatusCode)

		return
	}
}
