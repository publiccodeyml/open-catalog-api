package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnalysisNumbersOutsideStorableRange checks that numbers PostgreSQL
// can't store as numeric are refused with 422 on both dialects, instead of
// failing the write with 500 on PostgreSQL only.
func TestAnalysisNumbersOutsideStorableRange(t *testing.T) {
	for _, resource := range []struct{ table, id, title string }{
		{"software", rootSoftwareID, "can't update Software analysis"},
		{"catalogs", swissID, "can't update Catalog analysis"},
	} {
		t.Run(resource.table, func(t *testing.T) {
			loadFixtures(t)

			patch := func(number string) (int, map[string]any) {
				t.Helper()

				body := `{"ns":{"v":1,"x":` + number + `}}`
				req, err := newTestRequest(http.MethodPatch, "/v1/"+resource.table+"/"+resource.id+"/analysis",
					strings.NewReader(body))
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

			for number, detail := range map[string]string{
				"1e131072":                        "numbers must fit a float64 without rounding to zero or infinity",
				"-1e1000000":                      "numbers must fit a float64 without rounding to zero or infinity",
				"1e-20000":                        "numbers must fit a float64 without rounding to zero or infinity",
				"1" + strings.Repeat("0", 140000): "numbers must fit a float64 without rounding to zero or infinity",
				"0e-20000":                        "numbers must have an exponent between -16383 and 16383",
				"1." + strings.Repeat("0", 20000): "numbers must not have more than 16383 digits after the decimal point",
			} {
				code, result := patch(number)

				assert.Equal(t, http.StatusUnprocessableEntity, code, number[:min(len(number), 16)])
				assert.Equal(t, resource.title, result["title"])
				assert.Equal(t, "analysis.ns: "+detail, result["detail"])
			}

			code, result := patch("1.7976931348623157e308")
			require.Equal(t, http.StatusOK, code, result)

			// The merged document holds only this namespace, so none of the
			// refused patches was stored.
			assert.Len(t, result, 1)

			namespace, ok := result["ns"].(map[string]any)
			require.True(t, ok, result)
			assert.InDelta(t, 1.7976931348623157e308, namespace["x"], 0)
		})
	}
}
