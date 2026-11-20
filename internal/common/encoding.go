package common

import (
	"bytes"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
)

// RefuseEncodedBodies answers 415 when a request body declares a content
// coding other than identity. Fiber decodes gzip, deflate and brotli bodies
// with no size limit, and BodyLimit only counts the encoded bytes, so a
// small compressed body could make the server allocate gigabytes. The
// header is removed once accepted, so the body is always read as sent.
func RefuseEncodedBodies(ctx *fiber.Ctx) error {
	header := &ctx.Request().Header

	if len(ctx.Request().Body()) > 0 && !identityCodingOnly(header.PeekAll(fiber.HeaderContentEncoding)) {
		ctx.Set(fiber.HeaderAcceptEncoding, "identity")

		return Error(fiber.StatusUnsupportedMediaType, utils.StatusMessage(fiber.StatusUnsupportedMediaType),
			"request bodies must not be compressed: send them without a Content-Encoding")
	}

	header.Del(fiber.HeaderContentEncoding)

	return ctx.Next()
}

func identityCodingOnly(values [][]byte) bool {
	for _, value := range values {
		for coding := range bytes.SplitSeq(value, []byte(",")) {
			coding = bytes.TrimSpace(coding)
			if len(coding) > 0 && !bytes.EqualFold(coding, []byte("identity")) {
				return false
			}
		}
	}

	return true
}
