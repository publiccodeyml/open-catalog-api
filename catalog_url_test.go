package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSoftwareURLsBelongToCatalog(t *testing.T) {
	loadFixtures(t)
	request := func(method, path, body string, status int) map[string]any {
		t.Helper()
		req, err := newTestRequest(method, path, strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", goodToken)
		req.Header.Set("Content-Type", "application/json")
		res, err := app.Test(req, -1)
		require.NoError(t, err)
		defer res.Body.Close()
		var result map[string]any
		if status != 204 {
			require.NoError(t, json.NewDecoder(res.Body).Decode(&result))
		}
		require.Equal(t, status, res.StatusCode, "%s %s: %v", method, path, result)
		return result
	}
	const canonical = "https://shared.example.org/repo"
	const alias = "https://shared.example.org/old"
	body := `{"url":"` + canonical + `","aliases":["` + alias + `"],"publiccodeYml":"-"}`
	paths := []string{"/v1/catalogs/italia/software", "/v1/catalogs/swiss/software", "/v1/catalogs/%E2%88%85/software"}
	ids := make([]string, len(paths))
	for i, path := range paths {
		created := request("POST", path, body, 200)
		ids[i] = created["id"].(string)
		request("POST", path, body, 409)
		// A canonical URL cannot collide with another record's alias.
		request("POST", path, `{"url":"`+alias+`","publiccodeYml":"-"}`, 409)
		other := request("POST", path, `{"url":"https://other.example.org/repo","publiccodeYml":"-"}`, 200)
		request("PATCH", path+"/"+other["id"].(string), `{"aliases":["`+canonical+`"]}`, 409)
	}
	// The global POST endpoint and the root catalog share one namespace.
	request("POST", "/v1/software", body, 409)
	for i, path := range paths {
		for _, u := range []string{canonical, alias} {
			result := request("GET", path+"?url="+url.QueryEscape(u)+"&scopeTest=initial", "", 200)
			data := assertListResponse(t, result)
			require.Len(t, data, 1)
			assert.Equal(t, ids[i], data[0]["id"])
		}
	}
	result := request("GET", "/v1/software?url="+url.QueryEscape(alias)+"&scopeTest=global", "", 200)
	require.Len(t, assertListResponse(t, result), 3)
	// PATCH-created URLs inherit ownership and may be shared across catalogs.
	for i, path := range paths {
		patched := request("PATCH", path+"/"+ids[i], `{"url":"`+alias+`","aliases":["https://shared.example.org/new"]}`, 200)
		assert.Equal(t, alias, patched["url"])
		result := request("GET", path+"?url=https://shared.example.org/new&scopeTest=patched", "", 200)
		data := assertListResponse(t, result)
		require.Len(t, data, 1)
		assert.Equal(t, ids[i], data[0]["id"])
	}
	request("DELETE", "/v1/software/"+ids[0], "", 204)
	result = request("GET", "/v1/software?url="+url.QueryEscape(alias)+"&scopeTest=deleted", "", 200)
	require.Len(t, assertListResponse(t, result), 2)
}
