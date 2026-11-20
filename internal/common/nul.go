package common

import (
	"bytes"
	"encoding/json/jsontext"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
)

// nulEscape is the only way a JSON document can carry U+0000 in a string:
// a raw 0x00 byte is not valid JSON and the decoders refuse it already.
const nulEscape = `\u0000`

// CheckNULCharacters answers 422 when a string in the JSON body, member
// names included, contains U+0000. PostgreSQL text and jsonb columns refuse
// it, so the write would fail with a 500. The detail names the string by
// its JSON pointer. A body that is not valid JSON passes: the decoder that
// runs next reports it.
func CheckNULCharacters(body []byte, errorMessage string) error {
	if !bytes.Contains(body, []byte(nulEscape)) {
		return nil
	}

	decoder := jsontext.NewDecoder(bytes.NewReader(body))

	for {
		token, err := decoder.ReadToken()
		if err != nil {
			return nil //nolint:nilerr // invalid JSON is reported by the decoder that runs next
		}

		if token.Kind() == '"' && strings.ContainsRune(token.String(), 0) {
			pointer := string(decoder.StackPointer())
			if pointer == "" {
				pointer = "/"
			}

			return Error(fiber.StatusUnprocessableEntity, errorMessage,
				"the string at "+pointer+" must not contain NUL characters (U+0000)")
		}
	}
}

// RefuseNULInURL answers 400 when the decoded path or a query parameter
// name or value contains a NUL character. The database refuses it in a
// lookup, which would otherwise fail with a 500 even on requests that need
// no token.
func RefuseNULInURL(ctx *fiber.Ctx) error {
	uri := ctx.Request().URI()

	found := bytes.IndexByte(uri.Path(), 0) >= 0

	for key, value := range uri.QueryArgs().All() {
		if bytes.IndexByte(key, 0) >= 0 || bytes.IndexByte(value, 0) >= 0 {
			found = true

			break
		}
	}

	if found {
		return Error(fiber.StatusBadRequest, utils.StatusMessage(fiber.StatusBadRequest),
			"the URL must not contain NUL characters (%00)")
	}

	return ctx.Next()
}
