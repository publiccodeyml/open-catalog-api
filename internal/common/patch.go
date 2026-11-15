package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	jsonpatch "github.com/evanphx/json-patch/v5"
)

const (
	ContentTypeJSONPatch = "application/json-patch+json"

	maxPatchOps     = 100
	maxPatchPathLen = 255
)

var (
	errPatchEmpty     = errors.New("patch must contain at least one operation")
	errPatchTooMany   = fmt.Errorf("patch must not exceed %d operations", maxPatchOps)
	errPatchPathLen   = fmt.Errorf("path and from must not exceed %d characters", maxPatchPathLen)
	errPatchProtected = errors.New("operation not allowed on a read-only field")
)

type PatchError struct {
	Code int
	Err  error
}

func (e *PatchError) Error() string { return e.Err.Error() }
func (e *PatchError) Unwrap() error { return e.Err }

func IsJSONPatch(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)

	return err == nil && mediaType == ContentTypeJSONPatch
}

func ApplyPatch[T any](entity *T, contentType string, body []byte) (T, *PatchError) { //nolint:ireturn
	var zero T

	entityJSON, err := json.Marshal(entity)
	if err != nil {
		return zero, &PatchError{Code: http.StatusInternalServerError, Err: err}
	}

	var updatedJSON []byte

	if IsJSONPatch(contentType) { //nolint:nestif
		patch, err := jsonpatch.DecodePatch(body)
		if err != nil {
			return zero, &PatchError{Code: http.StatusBadRequest, Err: err}
		}

		if err := validatePatch(patch); err != nil {
			return zero, &PatchError{Code: http.StatusUnprocessableEntity, Err: err}
		}

		updatedJSON, err = patch.Apply(entityJSON)
		if err != nil {
			return zero, &PatchError{Code: http.StatusUnprocessableEntity, Err: err}
		}

		if err := checkProtectedFields(entityJSON, updatedJSON); err != nil {
			return zero, &PatchError{Code: http.StatusUnprocessableEntity, Err: err}
		}
	} else {
		updatedJSON, err = jsonpatch.MergePatch(entityJSON, body)
		if err != nil {
			return zero, &PatchError{Code: http.StatusBadRequest, Err: err}
		}
	}

	var updated T
	if err := json.Unmarshal(updatedJSON, &updated); err != nil {
		code := http.StatusInternalServerError
		if IsJSONPatch(contentType) {
			code = http.StatusUnprocessableEntity
		}

		return zero, &PatchError{Code: code, Err: err}
	}

	return updated, nil
}

func validatePatch(patch jsonpatch.Patch) error {
	if len(patch) < 1 {
		return errPatchEmpty
	}

	if len(patch) > maxPatchOps {
		return errPatchTooMany
	}

	for _, operation := range patch {
		if err := validateOperation(operation); err != nil {
			return err
		}
	}

	return nil
}

func validateOperation(operation jsonpatch.Operation) error {
	path, pathErr := operation.Path()
	if pathErr == nil && len(path) > maxPatchPathLen {
		return errPatchPathLen
	}

	from, fromErr := operation.From()
	if fromErr == nil && len(from) > maxPatchPathLen {
		return errPatchPathLen
	}

	// A test on a protected path reads it without changing anything.
	if operation.Kind() == "test" {
		return nil
	}

	if pathErr == nil && isProtectedPath(path) {
		return fmt.Errorf("%w: %s", errPatchProtected, path)
	}

	if fromErr == nil && isProtectedPath(from) {
		return fmt.Errorf("%w: %s", errPatchProtected, from)
	}

	return nil
}

// isProtectedPath names the fields the server owns on every resource. They
// show up in the JSON the patch runs over, so without this check an operation
// on them would apply and then be silently undone by the handler, and a client
// would have no way to tell. createdAt also keys the list cursors, so moving
// it would reorder every listing.
func isProtectedPath(path string) bool {
	switch path {
	case "/id", "/createdAt", "/updatedAt", "/catalogId", "/analysis":
		return true
	}

	// Analysis is written through its dedicated endpoint.
	if strings.HasPrefix(path, "/analysis/") {
		return true
	}

	parts := strings.Split(path, "/")

	return len(parts) == 4 && parts[1] == "codeHosting" && parts[2] != "" &&
		(parts[3] == "createdAt" || parts[3] == "updatedAt")
}

const (
	fieldCreatedAt = "createdAt"
	fieldUpdatedAt = "updatedAt"
)

// checkProtectedFields compares the server owned fields of the patched
// document with the original. The path check misses an operation on a
// parent of a protected field, such as a replace of the root "" or of
// /codeHosting/0, which can carry a new id or createdAt in its value.
func checkProtectedFields(originalJSON, updatedJSON []byte) error {
	var original, updated map[string]any

	if err := json.Unmarshal(originalJSON, &original); err != nil {
		return err //nolint:wrapcheck
	}

	if err := json.Unmarshal(updatedJSON, &updated); err != nil {
		return err //nolint:wrapcheck
	}

	for _, field := range [...]string{"id", "catalogId", fieldCreatedAt, fieldUpdatedAt} {
		before, hadField := original[field]
		after, hasField := updated[field]

		// Every serialized resource carries these fields, so a document
		// without one asked for its removal.
		if hadField != hasField || !reflect.DeepEqual(before, after) {
			return fmt.Errorf("%w: /%s", errPatchProtected, field)
		}
	}

	return checkCodeHostingTimestamps(original["codeHosting"], updated["codeHosting"])
}

// checkCodeHostingTimestamps refuses a code hosting timestamp that differs
// from the stored entry with the same URL, which is how the publisher handler
// matches entries, and from the stored entry at the same index, which an
// operation on /codeHosting/0/url leaves in place. An entry without
// timestamps is fine: clients list code hosting by URL and group alone.
func checkCodeHostingTimestamps(original, updated any) error {
	updatedEntries, ok := updated.([]any)
	if !ok {
		return nil
	}

	originalEntries, _ := original.([]any)
	byURL := map[string]map[string]any{}

	for _, entry := range originalEntries {
		if fields, ok := entry.(map[string]any); ok {
			if url, ok := fields["url"].(string); ok {
				byURL[url] = fields
			}
		}
	}

	for index, entry := range updatedEntries {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		url, _ := fields["url"].(string)

		var atIndex map[string]any
		if index < len(originalEntries) {
			atIndex, _ = originalEntries[index].(map[string]any)
		}

		if field := changedTimestamp(fields, byURL[url], atIndex); field != "" {
			return fmt.Errorf("%w: /codeHosting/%s/%s", errPatchProtected, strconv.Itoa(index), field)
		}
	}

	return nil
}

// changedTimestamp names the first timestamp of entry that matches neither
// stored entry, or returns "" when there is none.
func changedTimestamp(entry, sameURL, sameIndex map[string]any) string {
	for _, field := range [...]string{fieldCreatedAt, fieldUpdatedAt} {
		value, present := entry[field]
		if present && !storedValue(sameURL, field, value) && !storedValue(sameIndex, field, value) {
			return field
		}
	}

	return ""
}

func storedValue(entry map[string]any, field string, value any) bool {
	stored, ok := entry[field]

	return ok && reflect.DeepEqual(stored, value)
}
