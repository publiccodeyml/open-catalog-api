package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/o1egl/paseto"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPasetoMiddlewareValidatesTemporalClaims(t *testing.T) {
	t.Parallel()

	key := common.Base64Key([]byte("test-paseto-key-dont-use-in-prod"))
	now := time.Now().UTC()

	tests := []struct {
		name       string
		payload    paseto.JSONToken
		statusCode int
	}{
		{
			name: "valid token",
			payload: paseto.JSONToken{
				IssuedAt:   now.Add(-time.Minute),
				NotBefore:  now.Add(-time.Minute),
				Expiration: now.Add(time.Minute),
			},
			statusCode: fiber.StatusNoContent,
		},
		{
			name: "token without expiration",
			payload: paseto.JSONToken{
				IssuedAt: now.Add(-time.Minute),
			},
			statusCode: fiber.StatusNoContent,
		},
		{
			name: "expired token",
			payload: paseto.JSONToken{
				IssuedAt:   now.Add(-2 * time.Minute),
				Expiration: now.Add(-time.Minute),
			},
			statusCode: fiber.StatusUnauthorized,
		},
		{
			name: "token not valid yet",
			payload: paseto.JSONToken{
				IssuedAt:  now.Add(-time.Minute),
				NotBefore: now.Add(time.Minute),
			},
			statusCode: fiber.StatusUnauthorized,
		},
		{
			name: "token issued in the future",
			payload: paseto.JSONToken{
				IssuedAt: now.Add(time.Minute),
			},
			statusCode: fiber.StatusUnauthorized,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			token, err := paseto.NewV2().Encrypt(key[:], test.payload, nil)
			require.NoError(t, err)

			app := fiber.New()
			app.Use(NewPasetoMiddleware(common.Environment{PasetoKey: &key}, func(string, string) bool { return true }))
			app.Post("/", func(ctx *fiber.Ctx) error {
				return ctx.SendStatus(fiber.StatusNoContent)
			})

			request := httptest.NewRequest(http.MethodPost, "/", nil)
			request.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)

			response, err := app.Test(request, -1)
			require.NoError(t, err)
			defer response.Body.Close()

			assert.Equal(t, test.statusCode, response.StatusCode)
		})
	}
}
