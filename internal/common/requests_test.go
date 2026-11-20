package common

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// www removal
		{"https://www.example.org", "https://example.org"},
		// trailing slash removal
		{"https://example.org/path/", "https://example.org/path"},
		// host lowercased
		{"https://EXAMPLE.ORG/path", "https://example.org/path"},
		// scheme lowercased
		{"HTTPS://example.org/path", "https://example.org/path"},
		// default port removal
		{"https://example.org:443/path", "https://example.org/path"},
		// dot segments resolved
		{"https://example.org/a/../b", "https://example.org/b"},
		// www + trailing slash combined
		{"https://www.example.org/repo/", "https://example.org/repo"},
		// already normalized: no change
		{"https://example.org/repo", "https://example.org/repo"},
		// invalid URL: returned as-is
		{"not-a-valid-url", "not-a-valid-url"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, NormalizeURL(tt.input))
		})
	}
}

func TestRequestLengthLimits(t *testing.T) {
	url255 := "https://example.com/" + strings.Repeat("a", 235)
	url256 := url255 + "a"
	// An IP literal keeps the webhook target check away from DNS.
	webhookURL255 := "https://8.8.8.8/" + strings.Repeat("a", 239)
	text255 := strings.Repeat("a", 255)
	text256 := text255 + "a"
	text99999 := strings.Repeat("a", 99999)
	text100000 := text99999 + "a"
	email255 := strings.Repeat("a", 64) + "@" + strings.Repeat("b", 63) + "." +
		strings.Repeat("c", 63) + "." + strings.Repeat("d", 58) + ".org"
	email256 := "a" + email255

	tooManyAliases := make([]string, 256)
	for i := range tooManyAliases {
		tooManyAliases[i] = "https://example.com/" + strconv.Itoa(i)
	}

	codeHosting := []CodeHosting{{URL: "https://example.com/"}}
	codeHosting255 := []CodeHosting{{URL: "https://example.com/" + strings.Repeat("a", 235)}}

	tooManyCodeHosting := make([]CodeHosting, 256)
	for i := range tooManyCodeHosting {
		tooManyCodeHosting[i] = CodeHosting{URL: "https://example.com/" + strconv.Itoa(i)}
	}

	tests := []struct {
		name    string
		request any
		rule    string
	}{
		{"one character log message", Log{Message: "a"}, ""},
		{"log message at the limit", Log{Message: strings.Repeat("a", 2048)}, ""},
		{"log message over the limit", Log{Message: strings.Repeat("a", 2049)}, "max"},
		{"empty log message", Log{}, "required"},
		{"webhook URL at the limit", Webhook{URL: webhookURL255}, ""},
		{"webhook URL over the limit", Webhook{URL: webhookURL255 + "a"}, "max"},
		{"software URL at the limit", SoftwarePost{URL: url255, PubliccodeYml: "x"}, ""},
		{"software URL over the limit", SoftwarePost{URL: url256, PubliccodeYml: "x"}, "max"},
		{"software alias over the limit", SoftwarePost{URL: url255, Aliases: []string{url256}, PubliccodeYml: "x"}, "max"},
		{"patched software URL over the limit", SoftwarePatch{URL: &url256}, "max"},
		{"patched software alias over the limit", SoftwarePatch{Aliases: &[]string{url256}}, "max"},
		{"too many software aliases", SoftwarePost{URL: url255, Aliases: tooManyAliases, PubliccodeYml: "x"}, "max"},
		{"patched software with too many aliases", SoftwarePatch{Aliases: &tooManyAliases}, "max"},
		{"publiccode.yml at the limit", SoftwarePost{URL: url255, PubliccodeYml: text99999}, ""},
		{"publiccode.yml over the limit", SoftwarePost{URL: url255, PubliccodeYml: text100000}, "max"},
		{"patched publiccode.yml over the limit", SoftwarePatch{PubliccodeYml: &text100000}, "max"},
		{"vitality at the limit", SoftwarePost{URL: url255, PubliccodeYml: "x", Vitality: &text99999}, ""},
		{"vitality over the limit", SoftwarePost{URL: url255, PubliccodeYml: "x", Vitality: &text100000}, "max"},
		{"patched vitality over the limit", SoftwarePatch{Vitality: &text100000}, "max"},
		{"publisher at the limits", PublisherPost{
			CodeHosting: codeHosting255, Description: text255, Email: &email255,
		}, ""},
		{"publisher description over the limit", PublisherPost{CodeHosting: codeHosting, Description: text256}, "max"},
		{"publisher email over the limit", PublisherPost{
			CodeHosting: codeHosting, Description: "x", Email: &email256,
		}, "max"},
		{"code hosting URL over the limit", PublisherPost{
			CodeHosting: []CodeHosting{{URL: url256}}, Description: "x",
		}, "max"},
		{"too many code hosting URLs", PublisherPost{CodeHosting: tooManyCodeHosting, Description: "x"}, "max"},
		{"patched publisher description over the limit", PublisherPatch{Description: &text256}, "max"},
		{"patched publisher email over the limit", PublisherPatch{Email: &email256}, "max"},
		{"patched publisher with too many code hosting URLs", PublisherPatch{CodeHosting: &tooManyCodeHosting}, "max"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateStruct(tt.request)
			if tt.rule == "" {
				assert.Empty(t, errs)

				return
			}

			require.Len(t, errs, 1)
			assert.Equal(t, tt.rule, errs[0].Rule)
		})
	}
}

func TestURLLimitsApplyToTheNormalizedURL(t *testing.T) {
	// Each Cyrillic letter is stored percent-encoded, six characters long.
	grows := "https://example.org/" + strings.Repeat("ж", 45)
	growsWebhook := "https://8.8.8.8/" + strings.Repeat("ж", 45)
	// Dropping www. and the trailing slash brings 256 characters to 251.
	shrinks := "https://www.example.org/" + strings.Repeat("a", 231) + "/"

	tests := []struct {
		name    string
		request any
		rule    string
	}{
		{"software URL", SoftwarePost{URL: grows, PubliccodeYml: "x"}, "max"},
		{"software alias", SoftwarePost{URL: "https://example.org", Aliases: []string{grows}, PubliccodeYml: "x"}, "max"},
		{"patched software URL", SoftwarePatch{URL: &grows}, "max"},
		{"patched software alias", SoftwarePatch{Aliases: &[]string{grows}}, "max"},
		{"code hosting URL", PublisherPost{CodeHosting: []CodeHosting{{URL: grows}}, Description: "x"}, "max"},
		{"webhook URL", Webhook{URL: growsWebhook}, "max"},
		{"catalog source URL", CatalogPost{
			Name: "x", Sources: []SourceInput{{URL: "https://example.org/" + strings.Repeat("ж", 339)}},
		}, "max"},
		{"URL that shrinks under the limit", SoftwarePost{URL: shrinks, PubliccodeYml: "x"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := ValidateStruct(tt.request)
			if tt.rule == "" {
				assert.Empty(t, errs)

				return
			}

			require.Len(t, errs, 1)
			assert.Equal(t, tt.rule, errs[0].Rule)
		})
	}
}

// A PATCH validates the stored URL again, so normalizing it a second time
// must not make it longer.
func TestNormalizeURLIsStable(t *testing.T) {
	for _, rawURL := range []string{
		"https://example.org/" + strings.Repeat("ж", 39),
		"https://example.org/a b/c%20d",
		"https://www.Example.org/x/../y/?b=2&a=1#frag",
	} {
		t.Run(rawURL, func(t *testing.T) {
			once := NormalizeURL(rawURL)
			assert.Equal(t, once, NormalizeURL(once))
		})
	}
}
