package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A PATCH listing a URL twice after normalization gets the 422 a POST
// gives, and leaves the stored URLs alone. The root software has the
// canonical url https://3-a.example.org/code/repo and the alias
// https://3-b.example.org/code/repo, the root publisher has the same two
// codeHosting urls, the first a group.
func TestSoftwarePatchRepeatedURLs(t *testing.T) {
	tests := []struct {
		name   string
		patch  func(t *testing.T, path, body string) (int, []byte)
		body   string
		detail string
	}{
		{
			name:   "merge patch alias repeats the canonical URL",
			patch:  patchMerge,
			body:   `{"aliases": ["https://www.3-a.example.org/code/repo/", "https://new.example.org"]}`,
			detail: "alias https://www.3-a.example.org/code/repo/ repeats url",
		},
		{
			name:   "merge patch alias repeats another alias",
			patch:  patchMerge,
			body:   `{"aliases": ["https://new.example.org", "https://www.new.example.org/"]}`,
			detail: "alias https://www.new.example.org/ repeats another alias",
		},
		{
			name:   "JSON Patch url moves onto a kept alias",
			patch:  patchJSON,
			body:   `[{"op": "replace", "path": "/url", "value": "https://www.3-b.example.org/code/repo/"}]`,
			detail: "alias https://3-b.example.org/code/repo repeats url",
		},
		{
			name:   "JSON Patch adds an alias that repeats another",
			patch:  patchJSON,
			body:   `[{"op": "add", "path": "/aliases/-", "value": "https://WWW.3-b.example.org/code/repo"}]`,
			detail: "alias https://WWW.3-b.example.org/code/repo repeats another alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadFixtures(t)

			code, body := tt.patch(t, "/v1/software/"+rootSoftwareID, tt.body)
			require.Equal(t, 422, code, "body: %s", body)

			response := decodeJSON(t, body)
			assert.Equal(t, "can't update Software", response["title"])
			assert.Equal(t, tt.detail, response["detail"])

			assert.Equal(t, 2, dbCount(t, "software_urls", "software_id", rootSoftwareID))
			assert.Equal(t, 0, dbCount(t, "software_urls", "url", "https://new.example.org"))
			assert.Equal(t, "https://3-a.example.org/code/repo",
				dbValue(t, "software_urls", "url", "id",
					dbValue(t, "software", "software_url_id", "id", rootSoftwareID)))
		})
	}
}

func TestPublishersPatchRepeatedCodeHosting(t *testing.T) {
	tests := []struct {
		name   string
		patch  func(t *testing.T, path, body string) (int, []byte)
		body   string
		detail string
	}{
		{
			name:  "merge patch repeats an entry with another group",
			patch: patchMerge,
			body: `{"codeHosting": [{"url": "https://dup.example.org", "group": true},
				{"url": "https://www.dup.example.org/", "group": false}]}`,
			detail: "codeHosting url https://www.dup.example.org/ repeats another entry",
		},
		{
			name:   "JSON Patch adds an entry that repeats a stored one",
			patch:  patchJSON,
			body:   `[{"op": "add", "path": "/codeHosting/-", "value": {"url": "https://www.3-a.example.org/code/repo/", "group": false}}]`,
			detail: "codeHosting url https://www.3-a.example.org/code/repo/ repeats another entry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loadFixtures(t)

			code, body := tt.patch(t, publisherPath+rootPublisherID, tt.body)
			require.Equal(t, 422, code, "body: %s", body)

			response := decodeJSON(t, body)
			assert.Equal(t, "can't update Publisher", response["title"])
			assert.Equal(t, tt.detail, response["detail"])

			assert.Equal(t, 2, dbCount(t, "publishers_code_hosting", "publisher_id", rootPublisherID))
			assert.Equal(t, 0, dbCount(t, "publishers_code_hosting", "url", "https://dup.example.org"))
			assert.Contains(t, []string{"true", "1"},
				dbValue(t, "publishers_code_hosting", `"group"`, "url", "https://3-a.example.org/code/repo"))
		})
	}
}
