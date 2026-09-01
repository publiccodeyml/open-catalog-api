package jsondecoder

import (
	"encoding/json/v2"
	"errors"
)

var ErrUnknownField = errors.New("unknown field in JSON input")

// UnmarshalDisallowUnknownFields parses the JSON-encoded data
// and stores the result in the value pointed to by v like json.Unmarshal,
// but rejecting unknown fields for extra security.
//
// json/v2 is also stricter than encoding/json out of the box: it errors
// on trailing data after the top-level value, duplicate object member
// names and member names differing in case from the struct tag.
func UnmarshalDisallowUnknownFields(data []byte, v any) error {
	if err := json.Unmarshal(data, v, json.RejectUnknownMembers(true)); err != nil {
		if errors.Is(err, json.ErrUnknownName) {
			return ErrUnknownField
		}

		// we want to provide an alternative implementation, with the
		// unwrapped errors
		//nolint:wrapcheck
		return err
	}

	return nil
}
