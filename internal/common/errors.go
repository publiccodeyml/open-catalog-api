package common

import (
	"errors"
	"log"
	"net/http"

	"github.com/gofiber/fiber/v2"
)

var (
	ErrAuthentication  = errors.New("token authentication failed")
	ErrInvalidDateTime = errors.New("invalid date time format (RFC 3339 needed)")
	ErrKeyLen          = errors.New("PASETO_KEY must be 32 bytes long once base64-decoded")
)

func InternalServerError(title string) ProblemJSONError {
	return Error(fiber.StatusInternalServerError, title, fiber.ErrInternalServerError.Message)
}

func Error(status int, title string, detail string) ProblemJSONError {
	return ProblemJSONError{Title: title, Detail: detail, Status: status}
}

func ErrorWithValidationErrors(
	status int, title string, validationErrors []ValidationError,
) ProblemJSONError {
	detail := GenerateErrorDetails(validationErrors)

	return ProblemJSONError{Title: title, Detail: detail, Status: status, ValidationErrors: validationErrors}
}

func CustomErrorHandler(ctx *fiber.Ctx, err error) error {
	var problemJSON *ProblemJSONError

	if e, ok := errors.AsType[*fiber.Error](err); ok {
		problemJSON = &ProblemJSONError{Status: e.Code, Title: http.StatusText(e.Code), Detail: e.Message}
	}

	if errors.Is(err, ErrAuthentication) {
		problemJSON = &ProblemJSONError{Status: fiber.StatusUnauthorized, Title: err.Error()}
	}

	if problemJSON == nil {
		//nolint:errorlint
		switch typed := err.(type) {
		case ProblemJSONError:
			problemJSON = &typed
		default:
			// Nothing classified this error, so it is a failure of the
			// server, and its text can carry the schema or the failing
			// statement: it goes to the log, the client gets a generic 500.
			log.Printf("unhandled error on %s %s: %s", ctx.Method(), ctx.Path(), typed)

			problemJSON = &ProblemJSONError{
				Status: fiber.StatusInternalServerError,
				Title:  fiber.ErrInternalServerError.Message,
				Detail: fiber.ErrInternalServerError.Message,
			}
		}
	}

	ctx.Status(problemJSON.Status)

	return ctx.JSON(problemJSON, "application/problem+json")
}
