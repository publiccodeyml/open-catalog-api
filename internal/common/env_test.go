package common

import (
	"math"
	"os"
	"testing"

	"github.com/caarlos0/env/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestTrustedProxiesEnvironment(t *testing.T) {
	for _, test := range []struct {
		value string
		want  []string
		err   string
	}{
		{value: "10.9.9.9,127.0.0.1", want: []string{"10.9.9.9", "127.0.0.1"}},
		{value: "10.9.9.9, 127.0.0.1 ", want: []string{"10.9.9.9", "127.0.0.1"}},
		{value: "10.0.0.0/8,", want: []string{"10.0.0.0/8"}},
		{value: "2001:DB8:0:0::1,::ffff:192.0.2.1", want: []string{"2001:db8::1", "192.0.2.1"}},
		{value: "2001:db8::/32", want: []string{"2001:db8::/32"}},
		{value: "localhost", err: `TRUSTED_PROXIES entries must be IP addresses or CIDRs: "localhost"`},
		{value: "10.9.9.9, 127.0.0.1/33", err: `TRUSTED_PROXIES entries must be IP addresses or CIDRs: " 127.0.0.1/33"`},
		{value: "127.0.0.1:8080", err: `TRUSTED_PROXIES entries must be IP addresses or CIDRs: "127.0.0.1:8080"`},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXIES", test.value)

			var config Environment
			require.NoError(t, env.Parse(&config))

			err := config.Validate()
			if test.err != "" {
				assert.EqualError(t, err, test.err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, test.want, config.TrustedProxies)
		})
	}
}

func TestDatabasePoolEnvironment(t *testing.T) {
	for _, test := range []struct {
		value string
		want  int
	}{
		{"", 10}, {"3", 3},
	} {
		t.Run(test.value, func(t *testing.T) {
			t.Setenv("DATABASE_MAX_CONNECTIONS", test.value)
			if test.value == "" {
				require.NoError(t, os.Unsetenv("DATABASE_MAX_CONNECTIONS"))
			}

			var config Environment
			require.NoError(t, env.Parse(&config))
			assert.Equal(t, test.want, config.DatabaseMaxConnections)
		})
	}
}
