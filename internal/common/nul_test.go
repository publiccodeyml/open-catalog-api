package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckNULCharacters(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		detail string
	}{
		{"no NUL", `{"a":"b"}`, ""},
		{"escaped backslash", `{"a":"\\u0000"}`, ""},
		{"invalid JSON is left to the decoder", `{"a" "\u0000"}`, ""},
		{"nested value", `{"a":[1,{"b":"x\u0000"}]}`, "the string at /a/1/b must not contain NUL characters (U+0000)"},
		{"member name", `{"a\u0000":1}`, "the string at /a\u0000 must not contain NUL characters (U+0000)"},
		{"top level string", `"\u0000"`, "the string at / must not contain NUL characters (U+0000)"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := CheckNULCharacters([]byte(test.body), "title")
			if test.detail == "" {
				assert.NoError(t, err)

				return
			}

			assert.Equal(t, Error(422, "title", test.detail), err)
		})
	}
}
