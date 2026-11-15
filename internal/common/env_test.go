package common

import (
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
