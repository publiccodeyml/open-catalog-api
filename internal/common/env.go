package common

import (
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"time"
)

type Base64Key [SymmetricKeyLen]byte

var EnvironmentConfig Environment //nolint:gochecknoglobals

var (
	errMaxRequestsNegative        = errors.New("MAX_REQUESTS must not be negative")
	errWebhookDebounceNegative    = errors.New("WEBHOOK_DEBOUNCE_MS must not be negative")
	errWebhookDebounceMaxNegative = errors.New("WEBHOOK_DEBOUNCE_MAX_MS must not be negative")
	errEventRetentionNegative     = errors.New("EVENT_RETENTION_DAYS must not be negative")
	errEventRetentionOverflow     = fmt.Errorf("EVENT_RETENTION_DAYS must not exceed %d", math.MaxInt64/(24*time.Hour))
)

type Environment struct {
	MaxRequests            int        `env:"MAX_REQUESTS" envDefault:"0"`
	CurrentEnvironment     string     `env:"ENVIRONMENT" envDefault:"production"`
	DatabaseMaxConnections int        `env:"DATABASE_MAX_CONNECTIONS" envDefault:"10"`
	Database               string     `env:"DATABASE_DSN"`
	PasetoKey              *Base64Key `env:"PASETO_KEY"`

	// TrustedProxies is the list of addresses or CIDRs of the reverse
	// proxies in front of the API. X-Forwarded-For is trusted only on
	// requests coming from them: when the list is empty the header is
	// ignored and the client is the peer of the connection.
	TrustedProxies []string `env:"TRUSTED_PROXIES" envSeparator:","`

	// WebhookDebounceMS is the delay in milliseconds before a
	// webhook is dispatched after the last write. Set to 0 to disable
	// debouncing entirely. Note: debouncing is per replica.
	WebhookDebounceMS int `env:"WEBHOOK_DEBOUNCE_MS" envDefault:"1000"`

	// WebhookDebounceMaxMS is the hard cap in milliseconds on how long a
	// webhook can be deferred by repeated resets of the debounce timer.
	// Set to 0 to disable the cap. Ignored when WebhookDebounceMS is 0.
	WebhookDebounceMaxMS int `env:"WEBHOOK_DEBOUNCE_MAX_MS" envDefault:"10000"`

	// EventRetentionDays is how many days the audit trail is kept: a
	// background purge deletes the older events. Set to 0 to keep every
	// event forever.
	EventRetentionDays int `env:"EVENT_RETENTION_DAYS" envDefault:"365"`
}

func (k *Base64Key) UnmarshalText(text []byte) error {
	if len(text) == 0 {
		return nil
	}

	key, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		return fmt.Errorf("can't base64-decode PASETO_KEY environment variable: %w", err)
	}

	if len(key) != SymmetricKeyLen {
		return ErrKeyLen
	}

	*k = *(*[SymmetricKeyLen]byte)(key)

	return nil
}

// Validate rejects negative settings and retention windows that overflow a duration.
func (e *Environment) Validate() error {
	// The rate limiter replaces a non positive limit with its own default
	// of 5 requests, so a negative value would not disable it.
	if e.MaxRequests < 0 {
		return errMaxRequestsNegative
	}

	if e.WebhookDebounceMS < 0 {
		return errWebhookDebounceNegative
	}

	if e.WebhookDebounceMaxMS < 0 {
		return errWebhookDebounceMaxNegative
	}

	if e.EventRetentionDays < 0 {
		return errEventRetentionNegative
	}

	if e.EventRetentionDays > int(math.MaxInt64/(24*time.Hour)) {
		return errEventRetentionOverflow
	}

	return nil
}

func (e *Environment) IsTest() bool {
	return e.CurrentEnvironment == "test"
}
