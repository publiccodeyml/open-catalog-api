package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

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

func ApplyPatch[T any](entity *T, contentType string, body []byte) (T, *PatchError) { //nolint:ireturn
	var zero T

	entityJSON, err := json.Marshal(entity)
	if err != nil {
		return zero, &PatchError{Code: http.StatusInternalServerError, Err: err}
	}

	var updatedJSON []byte

	if contentType == ContentTypeJSONPatch { //nolint:nestif
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
	} else {
		updatedJSON, err = jsonpatch.MergePatch(entityJSON, body)
		if err != nil {
			return zero, &PatchError{Code: http.StatusInternalServerError, Err: err}
		}
	}

	var updated T
	if err := json.Unmarshal(updatedJSON, &updated); err != nil {
		return zero, &PatchError{Code: http.StatusInternalServerError, Err: err}
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
	case "/id", "/createdAt", "/updatedAt", "/catalogId":
		return true
	}

	return false
}
