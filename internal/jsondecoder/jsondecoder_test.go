package jsondecoder_test

import (
	"testing"

	"github.com/publiccodeyml/open-catalog-api/internal/jsondecoder"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalDisallowUnknownFields(t *testing.T) {
	tests := []struct {
		description string
		input       string
		wantError   bool
		unknown     bool
	}{
		{
			description: "valid member",
			input:       `{"name":"a"}`,
		},
		{
			description: "duplicate member",
			input:       `{"name":"a","name":"b"}`,
			wantError:   true,
		},
		{
			description: "differently cased member",
			input:       `{"Name":"a"}`,
			wantError:   true,
			unknown:     true,
		},
		{
			description: "trailing data",
			input:       `{"name":"a"}x`,
			wantError:   true,
		},
	}

	for _, test := range tests {
		t.Run(test.description, func(t *testing.T) {
			var target struct {
				Name string `json:"name"`
			}

			err := jsondecoder.UnmarshalDisallowUnknownFields([]byte(test.input), &target)
			if test.wantError {
				require.Error(t, err)
				if test.unknown {
					assert.ErrorIs(t, err, jsondecoder.ErrUnknownField)
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, "a", target.Name)
		})
	}
}
