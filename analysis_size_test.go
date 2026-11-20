package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalysisDocumentSizeLimit(t *testing.T) {
	// namespace answers a namespace with a payload of size bytes.
	namespace := func(size int) string {
		return `{"v":1,"x":"` + strings.Repeat("a", size) + `"}`
	}

	for _, resource := range []struct{ table, id, title string }{
		{"software", rootSoftwareID, "can't update Software analysis"},
		{"catalogs", swissID, "can't update Catalog analysis"},
	} {
		t.Run(resource.table, func(t *testing.T) {
			loadFixtures(t)

			patch := func(body string) (int, map[string]any) {
				t.Helper()

				req, err := newTestRequest(http.MethodPatch, "/v1/"+resource.table+"/"+resource.id+"/analysis", strings.NewReader(body))
				require.NoError(t, err)
				req.Header.Set("Authorization", goodToken)
				req.Header.Set("Content-Type", "application/merge-patch+json")

				res, err := app.Test(req, -1)
				require.NoError(t, err)

				defer res.Body.Close()

				var result map[string]any
				require.NoError(t, json.NewDecoder(res.Body).Decode(&result))

				return res.StatusCode, result
			}
			stored := func() string {
				t.Helper()

				return dbValue(t, resource.table, "analysis", "id", resource.id)
			}
			assertTooLarge := func(code int, result map[string]any) {
				t.Helper()

				assert.Equal(t, http.StatusUnprocessableEntity, code)
				assert.Equal(t, resource.title, result["title"])
				assert.Contains(t, result["detail"],
					fmt.Sprintf("analysis must not exceed %d bytes once merged, this patch would make it ", common.MaxAnalysisSize))
			}

			code, _ := patch(`{"first":` + namespace(common.MaxAnalysisSize/2) + `}`)
			require.Equal(t, http.StatusOK, code)
			before := stored()

			// Each request is well below the body limit, the merged
			// document is not.
			code, result := patch(`{"second":` + namespace(common.MaxAnalysisSize/2) + `}`)
			assertTooLarge(code, result)
			assert.Equal(t, before, stored(), "a rejected merge must be rolled back")

			// Replacing a namespace counts its new size only.
			code, result = patch(`{"first":` + namespace(common.MaxAnalysisSize-200) + `}`)
			require.Equal(t, http.StatusOK, code)
			assert.Len(t, result, 1)

			code, result = patch(`{"first":` + namespace(common.MaxAnalysisSize) + `}`)
			assertTooLarge(code, result)

			code, result = patch(`{"first":null,"second":` + namespace(1000) + `}`)
			require.Equal(t, http.StatusOK, code)
			assert.Equal(t, []string{"second"}, keys(result))
		})
	}
}

// A document stored before the limit existed can still lose namespaces,
// but cannot gain any while it stays over the limit.
func TestAnalysisDocumentOverLimitCanShrink(t *testing.T) {
	loadFixtures(t)

	big := `{"v":1,"x":"` + strings.Repeat("a", common.MaxAnalysisSize) + `"}`
	_, err := db.Exec(
		"UPDATE software SET analysis = "+placeholder(1)+" WHERE id = "+placeholder(2),
		`{"old":`+big+`,"older":`+big+`,"kept":{"v":1}}`, rootSoftwareID,
	)
	require.NoError(t, err)

	patch := func(body string) int {
		t.Helper()

		req, err := newTestRequest(http.MethodPatch, "/v1/software/"+rootSoftwareID+"/analysis", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Authorization", goodToken)
		req.Header.Set("Content-Type", "application/merge-patch+json")

		res, err := app.Test(req, -1)
		require.NoError(t, err)
		res.Body.Close()

		return res.StatusCode
	}

	assert.Equal(t, http.StatusUnprocessableEntity, patch(`{"kept":{"v":2}}`))
	assert.Equal(t, http.StatusOK, patch(`{"older":null}`))
	assert.Equal(t, http.StatusOK, patch(`{"old":null}`))
	assert.Equal(t, http.StatusOK, patch(`{"kept":{"v":2}}`))
}

func keys(m map[string]any) []string {
	result := make([]string, 0, len(m))
	for key := range m {
		result = append(result, key)
	}

	return result
}
