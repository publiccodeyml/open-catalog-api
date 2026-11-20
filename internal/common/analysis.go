package common

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

var (
	errAnalysisNotObject    = errors.New("must be a JSON object")
	errAnalysisMissingV     = errors.New("missing required field 'v'")
	errAnalysisInvalidV     = errors.New("'v' must be an integer")
	errAnalysisVOutOfRange  = errors.New("'v' must be between 1 and 999")
	errAnalysisUnexpectedDB = errors.New("unexpected database value type for analysis")
	errAnalysisAddTimestamp = errors.New("could not add the 't' timestamp")
	errAnalysisTooDeep      = fmt.Errorf("must not nest objects and arrays deeper than %d levels", MaxAnalysisDepth)
)

// MaxAnalysisDepth is how many objects and arrays a namespace may nest,
// the namespace object itself included. SQLite refuses JSON nested deeper
// than 1000 levels, so a namespace must stay well below that to be merged
// and read back on both dialects.
const MaxAnalysisDepth = 64

// MaxAnalysisSize is how many bytes the analysis document of a software or
// catalog may take once merged, measured as compact JSON. Each request is
// already bounded by the body limit, but merging new namespaces request
// after request would otherwise grow the document without end.
const MaxAnalysisSize = 1 << 20

// AnalysisTooLargeError reports a merge whose result would exceed
// MaxAnalysisSize. Size is the length of the merged document.
type AnalysisTooLargeError struct {
	Size int
}

func (e AnalysisTooLargeError) Error() string {
	return fmt.Sprintf("analysis must not exceed %d bytes once merged, this patch would make it %d bytes",
		MaxAnalysisSize, e.Size)
}

// CheckAnalysisSize answers an AnalysisTooLargeError when merged, the
// document a patch produces, exceeds MaxAnalysisSize. A patch that only
// removes namespaces always passes, so a document stored before the limit
// existed can still be cleaned up.
func CheckAnalysisSize(patch, merged AnalysisData) error {
	if onlyRemovals(patch) {
		return nil
	}

	// RawMessage values are compacted on marshal, so the size does not
	// depend on how the database formats the stored document.
	data, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("can't marshal AnalysisData: %w", err)
	}

	if len(data) > MaxAnalysisSize {
		return AnalysisTooLargeError{Size: len(data)}
	}

	return nil
}

// onlyRemovals answers whether every namespace in patch is null.
func onlyRemovals(patch AnalysisData) bool {
	for _, raw := range patch {
		if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return false
		}
	}

	return true
}

// AnalysisData is a map of { namespace: arbitrary JSON object }
// External software components (scanners, enrichers, security checkers, etc.) write under
// their own key.
// Every namespace must have "v" (schema version, int).
type AnalysisData map[string]json.RawMessage //nolint:recvcheck

func (a AnalysisData) Value() (driver.Value, error) {
	if a == nil {
		return nil, nil //nolint:nilnil
	}

	data, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("can't marshal AnalysisData: %w", err)
	}

	return string(data), nil
}

func (a *AnalysisData) Scan(value any) error {
	if value == nil {
		*a = nil

		return nil
	}

	var data []byte

	switch v := value.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		return errAnalysisUnexpectedDB
	}

	//nolint:wrapcheck
	return json.Unmarshal(data, a)
}

// namespaceMeta holds the fields we validate, the rest of the namespace is opaque.
type namespaceMeta struct {
	V *json.RawMessage `json:"v"`
}

// WithTimestamps checks each namespace with checkNamespace,
// then injects "t" (current time, RFC 3339). Null values request namespace deletion.
func WithTimestamps(analysis AnalysisData, now time.Time) (AnalysisData, error) {
	if analysis == nil {
		return nil, nil //nolint:nilnil
	}

	tPatch, _ := json.Marshal(struct { //nolint:errchkjson
		T string `json:"t"`
	}{T: now.UTC().Format(time.RFC3339)})

	result := make(AnalysisData, len(analysis))

	for namespace, raw := range analysis {
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			result[namespace] = json.RawMessage("null")

			continue
		}

		if err := checkNamespace(raw); err != nil {
			return nil, fmt.Errorf("analysis.%s: %w", namespace, err)
		}

		// MergePatch fails only on invalid JSON and both inputs are
		// validated above. If it fails anyway, better an error than a
		// silently nulled namespace.
		merged, err := jsonpatch.MergePatch(raw, tPatch)
		if err != nil {
			return nil, fmt.Errorf("analysis.%s: %w", namespace, errAnalysisAddTimestamp)
		}

		result[namespace] = merged
	}

	return result, nil
}

// jsonDepth answers how deeply objects and arrays nest in data, 0 for a
// scalar. On invalid JSON it answers the depth reached before the error.
func jsonDepth(data []byte) int {
	decoder := json.NewDecoder(bytes.NewReader(data))
	depth, maxDepth := 0, 0

	for {
		token, err := decoder.Token()
		if err != nil {
			return maxDepth
		}

		switch token {
		case json.Delim('{'), json.Delim('['):
			depth++
			maxDepth = max(maxDepth, depth)
		case json.Delim('}'), json.Delim(']'):
			depth--
		}
	}
}

// checkNamespace checks that raw is a JSON object with an integer "v"
// between 1 and 999, nesting at most MaxAnalysisDepth levels.
func checkNamespace(raw json.RawMessage) error {
	if jsonDepth(raw) > MaxAnalysisDepth {
		return errAnalysisTooDeep
	}

	var meta namespaceMeta

	if err := json.Unmarshal(raw, &meta); err != nil {
		return errAnalysisNotObject
	}

	if meta.V == nil {
		return errAnalysisMissingV
	}

	var version int
	if err := json.Unmarshal(*meta.V, &version); err != nil {
		return errAnalysisInvalidV
	}

	if version < 1 || version > 999 {
		return errAnalysisVOutOfRange
	}

	return nil
}
