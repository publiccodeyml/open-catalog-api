package common

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnvironmentValidate(t *testing.T) {
	tests := []struct {
		name string
		env  Environment
		err  string
	}{
		{"zero values", Environment{}, ""},
		{"negative request limit", Environment{MaxRequests: -1}, "MAX_REQUESTS must not be negative"},
		{"negative debounce delay", Environment{WebhookDebounceMS: -1}, "WEBHOOK_DEBOUNCE_MS must not be negative"},
		{"negative debounce cap", Environment{WebhookDebounceMaxMS: -1}, "WEBHOOK_DEBOUNCE_MAX_MS must not be negative"},
		{"default retention", Environment{EventRetentionDays: 365}, ""},
		{"maximum retention", Environment{EventRetentionDays: 106751}, ""},
		{"first overflowing retention", Environment{EventRetentionDays: 106752}, "EVENT_RETENTION_DAYS must not exceed 106751"},
		{"positive wrapped retention", Environment{EventRetentionDays: 213504}, "EVENT_RETENTION_DAYS must not exceed 106751"},
		{"maximum integer retention", Environment{EventRetentionDays: math.MaxInt}, "EVENT_RETENTION_DAYS must not exceed 106751"},
		{"negative retention", Environment{EventRetentionDays: -1}, "EVENT_RETENTION_DAYS must not be negative"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.env.Validate()
			if tt.err == "" {
				assert.NoError(t, err)
				return
			}

			assert.EqualError(t, err, tt.err)
		})
	}
}
