package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalysisNamespaceRemoval(t *testing.T) {
	for _, resource := range []struct{ table, id string }{
		{"software", "59803fb7-8eec-4fe5-a354-8926009c364a"},
		{"catalogs", italiaID},
	} {
		t.Run(resource.table, func(t *testing.T) {
			loadFixtures(t)
			patch := func(body string) map[string]any {
				t.Helper()
				req, err := newTestRequest(http.MethodPatch, "/v1/"+resource.table+"/"+resource.id+"/analysis", strings.NewReader(body))
				require.NoError(t, err)
				req.Header.Set("Authorization", goodToken)
				req.Header.Set("Content-Type", "application/merge-patch+json")
				res, err := app.Test(req, -1)
				require.NoError(t, err)
				defer res.Body.Close()
				require.Equal(t, http.StatusOK, res.StatusCode)
				var result map[string]any
				require.NoError(t, json.NewDecoder(res.Body).Decode(&result))
				var stored map[string]any
				require.NoError(t, json.Unmarshal([]byte(dbValue(t, resource.table, "analysis", "id", resource.id)), &stored))
				assert.Equal(t, result, stored)
				return result
			}
			assert.Empty(t, patch(`{"missing":null}`))
			seeded := patch(`{"retired":{"v":1},"keep":{"v":1,"nullable":null}}`)
			result := patch(`{"retired":null,"missing":null,"fresh":{"v":2,"nullable":null}}`)
			require.Len(t, result, 2)
			assert.NotContains(t, result, "retired")
			assert.NotContains(t, result, "missing")
			assert.Equal(t, seeded["keep"], result["keep"])
			fresh := result["fresh"].(map[string]any)
			assert.Contains(t, fresh, "nullable")
			assert.Nil(t, fresh["nullable"])
			assertRFC3339(t, fresh["t"])
			assert.Empty(t, patch(`{"keep":null,"fresh":null}`))
			assert.Empty(t, patch(`{"keep":null,"fresh":null}`))
		})
	}
}
