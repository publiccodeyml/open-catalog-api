package common

import (
	"fmt"
	"io"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
)

// LimitRequestBody reads the streamed request body into memory, refusing
// with 413 a body longer than limit bytes. The server streams request
// bodies because fasthttp otherwise allocates the declared Content-Length
// before running any handler, so request headers alone, without a token,
// cost the server the whole limit per connection. Streaming drops the
// server's own body limit, so it is enforced here, before any handler
// reads the body.
func LimitRequestBody(limit int) fiber.Handler {
	tooLarge := fmt.Sprintf("request bodies must not be longer than %d bytes", limit)

	return func(ctx *fiber.Ctx) error {
		request := ctx.Request()

		if request.Header.ContentLength() > limit {
			return Error(fiber.StatusRequestEntityTooLarge,
				utils.StatusMessage(fiber.StatusRequestEntityTooLarge), tooLarge)
		}

		stream := request.BodyStream()
		if stream == nil {
			return ctx.Next()
		}

		body, err := io.ReadAll(io.LimitReader(stream, int64(limit)+1))
		if err != nil {
			return Error(fiber.StatusBadRequest, utils.StatusMessage(fiber.StatusBadRequest),
				"the request body could not be read")
		}

		if len(body) > limit {
			return Error(fiber.StatusRequestEntityTooLarge,
				utils.StatusMessage(fiber.StatusRequestEntityTooLarge), tooLarge)
		}

		request.SetBodyRaw(body)

		return ctx.Next()
	}
}
