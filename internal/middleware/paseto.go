package middleware

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"log"

	pasetoware "github.com/gofiber/contrib/paseto"
	"github.com/gofiber/fiber/v2"
	"github.com/o1egl/paseto"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
)

func NewRandomPasetoKey() *common.Base64Key {
	key := make([]byte, common.SymmetricKeyLen)

	if _, err := rand.Read(key); err != nil {
		log.Fatalf("can't generate PASETO key: %s", err.Error())
	}

	return (*common.Base64Key)(key)
}

func NewPasetoMiddleware(
	envs common.Environment,
	requiresAuth func(method, path string) bool,
) fiber.Handler {
	return pasetoware.New(pasetoware.Config{
		TokenPrefix:  "Bearer",
		SymmetricKey: envs.PasetoKey[:],
		Next: func(ctx *fiber.Ctx) bool {
			return !requiresAuth(ctx.Method(), ctx.Path())
		},
		Validate: func(data []byte) (any, error) {
			var payload paseto.JSONToken
			if err := json.Unmarshal(data, &payload); err != nil {
				return nil, fmt.Errorf("can't unmarshal PASETO token: %w", err)
			}

			if err := payload.Validate(); err != nil {
				return nil, fmt.Errorf("can't validate PASETO token: %w", err)
			}

			return payload, nil
		},
		SuccessHandler: func(ctx *fiber.Ctx) error {
			// The token is the only place the identity of the caller comes
			// from, and the model hooks recording an event read it off the
			// request context.
			if payload, ok := ctx.Locals(pasetoware.DefaultContextKey).(paseto.JSONToken); ok {
				ctx.SetUserContext(common.WithActor(ctx.UserContext(), payload.Subject))
			}

			return ctx.Next()
		},
		ErrorHandler: func(ctx *fiber.Ctx, _ error) error {
			return common.CustomErrorHandler(ctx, common.ErrAuthentication)
		},
	})
}
