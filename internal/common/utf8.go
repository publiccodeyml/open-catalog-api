package common

import (
	"unicode/utf8"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
)

// RefuseInvalidUTF8InURL answers 404 when the decoded path and 400 when a
// query parameter name or value is not valid UTF-8. PostgreSQL refuses such
// bytes in a lookup, which would otherwise fail with a 500 even on requests
// that need no token. No resource can have such a path, since every stored
// string is valid UTF-8.
func RefuseInvalidUTF8InURL(ctx *fiber.Ctx) error {
	uri := ctx.Request().URI()

	if !utf8.Valid(uri.Path()) {
		return Error(fiber.StatusNotFound, utils.StatusMessage(fiber.StatusNotFound),
			"the path is not valid UTF-8")
	}

	for key, value := range uri.QueryArgs().All() {
		if !utf8.Valid(key) || !utf8.Valid(value) {
			return Error(fiber.StatusBadRequest, utils.StatusMessage(fiber.StatusBadRequest),
				"query parameters must be valid UTF-8")
		}
	}

	return ctx.Next()
}
