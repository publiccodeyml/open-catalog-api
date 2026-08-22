package general

import (
	"errors"
	"fmt"
	"testing"

	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/stretchr/testify/assert"
)

func TestQueryErrorDetail(t *testing.T) {
	tests := []struct {
		description string
		err         error
		expected    string
	}{
		{
			description: "an invalid date time keeps its own message",
			err:         common.ErrInvalidDateTime,
			expected:    "invalid date time format (RFC 3339 needed)",
		},
		{
			description: "a non integer page size keeps its own message",
			err:         errInvalidPageSize,
			expected:    "page[size] must be an integer",
		},
		{
			description: "an out of range page size keeps its own message",
			err:         errPageSizeOutOfRange,
			expected:    "page[size] must be between 1 and 100",
		},
		{
			description: "a wrapped known error loses the wrapping context",
			err:         fmt.Errorf("while paginating: %w", errInvalidPageSize),
			expected:    "page[size] must be an integer",
		},
		{
			description: "an unknown error is replaced",
			err:         errors.New(`pq: column "nope" does not exist`),
			expected:    "invalid query parameters",
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			assert.Equal(t, test.expected, QueryErrorDetail(test.err))
		})
	}
}
