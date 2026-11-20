package common

import (
	"encoding/json"
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
