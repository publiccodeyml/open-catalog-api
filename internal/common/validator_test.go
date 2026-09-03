package common

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsForbiddenIP covers the network-range helper directly.
func TestIsForbiddenIP(t *testing.T) {
	tests := []struct {
		ip        string
		forbidden bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"::1", true},
		{"0.0.0.0", true},
		{"169.254.169.254", true},
		{"169.254.0.1", true},
		{"fe80::1", true},
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"192.168.255.255", true},
		{"fc00::1", true},
		{"fdff::1", true},
		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"2001:4860:4860::8888", false},
	}

	for _, tt := range tests {
		t.Run(tt.ip, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			require.NotNil(t, ip)
			assert.Equal(t, tt.forbidden, IsForbiddenIP(ip))
		})
	}
}

// TestValidateWebhookURLStruct uses ValidateStruct so that the registered
// webhook_url tag is exercised through the full validation pipeline.
func TestValidateWebhookURLStruct(t *testing.T) {
	type webhookInput struct {
		URL string `json:"url" validate:"required,url,webhook_url"`
	}

	tests := []struct {
		name  string
		url   string
		valid bool
	}{
		{"valid https", "https://example.com/hook", true},
		{"valid https with path", "https://example.com/v1/events", true},
		{"http rejected", "http://example.com/hook", false},
		{"loopback IPv4", "https://127.0.0.1/hook", false},
		{"loopback ::1", "https://[::1]/hook", false},
		{"unspecified 0.0.0.0", "https://0.0.0.0/hook", false},
		{"link-local 169.254.169.254", "https://169.254.169.254/hook", false},
		{"RFC 1918 10.x", "https://10.0.0.1/hook", false},
		{"RFC 1918 172.16.x", "https://172.16.0.1/hook", false},
		{"RFC 1918 192.168.x", "https://192.168.1.1/hook", false},
		{"ULA fc00::1", "https://[fc00::1]/hook", false},
		{"userinfo rejected", "https://user:pass@example.com/hook", false},
		{"empty url", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateStruct(webhookInput{URL: tt.url})
			if tt.valid {
				assert.Empty(t, errs, "expected URL to be valid: %s", tt.url)
			} else {
				assert.NotEmpty(t, errs, "expected URL to be invalid: %s", tt.url)
			}
		})
	}
}

// TestValidateGitHubWebhook exercises the struct-level check tying the
// github format to the repository_dispatch endpoint.
func TestValidateGitHubWebhook(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		format string
		valid  bool
	}{
		{"dispatch endpoint", "https://api.github.com/repos/o/r/dispatches", "github", true},
		{"other github api path", "https://api.github.com/repos/o/r/issues", "github", false},
		{"missing repo", "https://api.github.com/repos/o//dispatches", "github", false},
		{"wrong host", "https://example.com/repos/o/r/dispatches", "github", false},
		{"default format any url", "https://example.com/hook", "", true},
		{"standard-webhooks any url", "https://example.com/hook", "standard-webhooks", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateStruct(Webhook{URL: tt.url, Format: tt.format, Secret: "0123456789abcdef"})
			if tt.valid {
				assert.Empty(t, errs, "expected valid: %s %s", tt.format, tt.url)
			} else {
				assert.NotEmpty(t, errs, "expected invalid: %s %s", tt.format, tt.url)
			}
		})
	}
}

func TestValidateCodeHostingURL(t *testing.T) {
	tests := []struct {
		name  string
		url   string
		valid bool
	}{
		{"https github", "https://github.com/foo/bar", true},
		{"http public", "http://example.org/repo", true},
		{"empty", "", true},
		{"userinfo with password", "https://user:pass@example.org/repo", true},
		{"userinfo no password", "https://user@example.org/repo", true},

		{"ftp scheme", "ftp://example.org/repo", false},
		{"file scheme", "file:///etc/passwd", false},
		{"git scheme", "git://example.org/repo.git", false},

		{"localhost host", "https://localhost/repo", false},
		{"localhost trailing dot", "https://localhost./repo", false},
		{"LOCALHOST trailing dot", "https://LOCALHOST./repo", false},
		{"loopback ipv4", "https://127.0.0.1/repo", false},
		{"loopback ipv6", "https://[::1]/repo", false},
		{"unspecified", "https://0.0.0.0/repo", false},

		{"private 10/8", "https://10.0.0.1/repo", false},
		{"private 172.16/12", "https://172.16.0.1/repo", false},
		{"private 192.168/16", "https://192.168.1.1/repo", false},
		{"link local v4", "https://169.254.1.1/repo", false},
		{"link local v6", "https://[fe80::1]/repo", false},

		{"missing host", "https:///repo", false},
		{"malformed", "::not a url::", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := struct {
				URL string `validate:"omitempty,http_url,code_hosting_url"`
			}{URL: tt.url}

			errs := ValidateStruct(payload)

			if tt.valid {
				assert.Empty(t, errs, "expected %q to validate", tt.url)
			} else {
				assert.NotEmpty(t, errs, "expected %q to fail", tt.url)
			}
		})
	}
}
