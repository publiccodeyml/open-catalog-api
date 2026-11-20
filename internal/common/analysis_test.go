package common

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithTimestamps(t *testing.T) {
	now := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	t.Run("null requests deletion without a timestamp", func(t *testing.T) {
		result, err := WithTimestamps(AnalysisData{"retired": json.RawMessage(" \nnull \t")}, now)
		require.NoError(t, err)
		assert.JSONEq(t, "null", string(result["retired"]))
	})

	t.Run("nil returns nil", func(t *testing.T) {
		result, err := WithTimestamps(nil, now)

		assert.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("injects t and preserves other fields", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 1, "score": 90}`),
		}

		result, err := WithTimestamps(input, now)
		require.NoError(t, err)

		var ns map[string]any
		require.NoError(t, json.Unmarshal(result["scanner"], &ns))

		assert.Equal(t, float64(1), ns["v"])
		assert.Equal(t, float64(90), ns["score"])
		assert.Equal(t, "2024-01-15T10:30:00Z", ns["t"])
	})

	t.Run("injects t into all namespaces", func(t *testing.T) {
		input := AnalysisData{
			"scanner":  json.RawMessage(`{"v": 1}`),
			"enricher": json.RawMessage(`{"v": 2}`),
		}

		result, err := WithTimestamps(input, now)
		require.NoError(t, err)

		for _, ns := range []string{"scanner", "enricher"} {
			var m map[string]any
			require.NoError(t, json.Unmarshal(result[ns], &m))
			assert.Equal(t, "2024-01-15T10:30:00Z", m["t"])
		}
	})

	t.Run("t overwrites existing t", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 1, "t": "2000-01-01T00:00:00Z"}`),
		}

		result, err := WithTimestamps(input, now)
		require.NoError(t, err)

		var ns map[string]any
		require.NoError(t, json.Unmarshal(result["scanner"], &ns))

		assert.Equal(t, "2024-01-15T10:30:00Z", ns["t"])
	})

	t.Run("missing v returns error", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`{"score": 90}`),
		}

		_, err := WithTimestamps(input, now)

		assert.ErrorIs(t, err, errAnalysisMissingV)
	})

	t.Run("v not integer returns error", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": "one"}`),
		}

		_, err := WithTimestamps(input, now)

		assert.ErrorIs(t, err, errAnalysisInvalidV)
	})

	t.Run("namespace not an object returns error", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`[1, 2, 3]`),
		}

		_, err := WithTimestamps(input, now)

		assert.ErrorIs(t, err, errAnalysisNotObject)
	})

	t.Run("v below 1 returns error", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 0}`),
		}

		_, err := WithTimestamps(input, now)

		assert.ErrorIs(t, err, errAnalysisVOutOfRange)
	})

	t.Run("v above 999 returns error", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 1000}`),
		}

		_, err := WithTimestamps(input, now)

		assert.ErrorIs(t, err, errAnalysisVOutOfRange)
	})

	t.Run("v at boundary 1 is valid", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 1}`),
		}

		_, err := WithTimestamps(input, now)

		assert.NoError(t, err)
	})

	t.Run("v at boundary 999 is valid", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 999}`),
		}

		_, err := WithTimestamps(input, now)

		assert.NoError(t, err)
	})

	t.Run("nesting at MaxAnalysisDepth is valid", func(t *testing.T) {
		deep := strings.Repeat("[", MaxAnalysisDepth-1) + strings.Repeat("]", MaxAnalysisDepth-1)
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 1, "x": ` + deep + `}`),
		}

		_, err := WithTimestamps(input, now)

		assert.NoError(t, err)
	})

	t.Run("nesting over MaxAnalysisDepth returns error", func(t *testing.T) {
		deep := strings.Repeat(`{"a":`, MaxAnalysisDepth) + "1" + strings.Repeat("}", MaxAnalysisDepth)
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 1, "x": ` + deep + `}`),
		}

		_, err := WithTimestamps(input, now)

		assert.ErrorIs(t, err, errAnalysisTooDeep)
		assert.EqualError(t, err, "analysis.scanner: must not nest objects and arrays deeper than 64 levels")
	})

	t.Run("brackets inside strings do not count as nesting", func(t *testing.T) {
		brackets := strings.Repeat("[{", MaxAnalysisDepth)
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 1, "x": "\\\"` + brackets + `"}`),
		}

		_, err := WithTimestamps(input, now)

		assert.NoError(t, err)
	})
}

func TestCheckNumber(t *testing.T) {
	zeros := strings.Repeat("0", maxAnalysisNumberScale)

	for _, tc := range []struct {
		literal string
		want    error
	}{
		{"0", nil},
		{"-0", nil},
		{"42", nil},
		{"-1.5", nil},
		{"1E+308", nil},
		{"1.7976931348623157e308", nil},
		{"4.9e-324", nil},
		{"0e16383", nil},
		{"0e-16383", nil},
		{"0." + zeros, nil},
		{"1e400", errAnalysisNumberRange},
		{"1e131072", errAnalysisNumberRange},
		{"-1e1000000", errAnalysisNumberRange},
		{"1" + strings.Repeat("0", 140000), errAnalysisNumberRange},
		{"1e-400", errAnalysisNumberRange},
		{"1e-20000", errAnalysisNumberRange},
		{"0." + zeros + "1", errAnalysisNumberRange},
		{"0e-20000", errAnalysisExponent},
		{"0e20000", errAnalysisExponent},
		{"0e99999999999999999999", errAnalysisExponent},
		{"0e-99999999999999999999", errAnalysisExponent},
		{"1." + zeros + "0", errAnalysisNumberScale},
		{"0." + zeros + "0", errAnalysisNumberScale},
		{"1." + zeros + "e-1", errAnalysisNumberScale},
		{"0.0e-16383", errAnalysisNumberScale},
	} {
		name := tc.literal
		if len(name) > 32 {
			name = fmt.Sprintf("%s... (%d bytes)", name[:16], len(name))
		}

		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, checkNumber(tc.literal))
		})
	}
}

func TestWithTimestampsNumbers(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	t.Run("refuses a number nested in an array", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 1, "x": [{"y": 1e400}]}`),
		}

		_, err := WithTimestamps(input, now)

		assert.EqualError(t, err,
			"analysis.scanner: numbers must fit a float64 without rounding to zero or infinity")
	})

	t.Run("refuses a number as v", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 1e-20000}`),
		}

		_, err := WithTimestamps(input, now)

		assert.ErrorIs(t, err, errAnalysisNumberRange)
	})

	t.Run("keeps numbers inside strings", func(t *testing.T) {
		input := AnalysisData{
			"scanner": json.RawMessage(`{"v": 1, "x": "1e400"}`),
		}

		_, err := WithTimestamps(input, now)

		assert.NoError(t, err)
	})
}

func TestCheckAnalysisSize(t *testing.T) {
	payload := func(size int) json.RawMessage {
		return json.RawMessage(`{"v":1,"x":"` + strings.Repeat("a", size) + `"}`)
	}
	// {"n":{"v":1,"x":""}} is 20 bytes around the payload.
	atLimit := AnalysisData{"n": payload(MaxAnalysisSize - 20)}
	overLimit := AnalysisData{"n": payload(MaxAnalysisSize - 19)}

	assert.NoError(t, CheckAnalysisSize(atLimit, atLimit))
	assert.Equal(t, AnalysisTooLargeError{Size: MaxAnalysisSize + 1}, CheckAnalysisSize(overLimit, overLimit))

	t.Run("indentation in stored namespaces does not count", func(t *testing.T) {
		indented := AnalysisData{"n": json.RawMessage("{\n  \"v\": 1,\n  \"x\": \"" +
			strings.Repeat("a", MaxAnalysisSize-20) + "\"\n}")}

		assert.NoError(t, CheckAnalysisSize(indented, indented))
	})

	t.Run("a patch that only removes namespaces passes", func(t *testing.T) {
		removal := AnalysisData{"gone": json.RawMessage("null")}

		assert.NoError(t, CheckAnalysisSize(removal, overLimit))
	})
}
