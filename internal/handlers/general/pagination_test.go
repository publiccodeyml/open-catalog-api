package general

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/pilagod/gorm-cursor-paginator/v2/paginator"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

func newTestCtx() (*fiber.App, *fiber.Ctx) {
	app := fiber.New()
	ctx := app.AcquireCtx(&fasthttp.RequestCtx{})
	return app, ctx
}

// TestNewPaginatorWithConfig_DoesNotMutateDefaultConfig verifies that calling
// NewPaginatorWithConfig with a custom Order does not mutate the global
// DefaultConfig, so subsequent callers still get the default ASC order.
func TestNewPaginatorWithConfig_DoesNotMutateDefaultConfig(t *testing.T) {
	app, ctx := newTestCtx()
	defer app.ReleaseCtx(ctx)

	_, err := NewPaginatorWithConfig(ctx, &paginator.Config{Order: paginator.DESC})
	assert.NoError(t, err)

	assert.Equal(t, paginator.ASC, DefaultConfig.Order,
		"DefaultConfig.Order must not be mutated by NewPaginatorWithConfig")
}

func TestPageSizeFromQueryCapsValuesAboveMax(t *testing.T) {
	app, ctx := newTestCtx()
	defer app.ReleaseCtx(ctx)

	ctx.Request().URI().SetQueryString("page[size]=200")

	size, err := pageSizeFromQuery(ctx)
	assert.NoError(t, err)
	assert.Equal(t, MaxLimitCount, size)
}

func TestPaginationLinksPreserveQueryNames(t *testing.T) {
	app := fiber.New()
	before, after := "previous+/=", "next+/="
	app.Get("/", func(ctx *fiber.Ctx) error {
		return ctx.JSON(fiber.Map{
			"all":     ctx.QueryBool("all", false),
			"queries": ctx.Queries(),
			"links":   NewPaginationLinks(ctx.Queries(), paginator.Cursor{Before: &before, After: &after}),
		})
	})
	type response struct {
		All     bool               `json:"all"`
		Queries map[string]string  `json:"queries"`
		Links   map[string]*string `json:"links"`
	}
	get := func(query string) response {
		t.Helper()
		req := httptest.NewRequest("GET", "/"+query, nil)
		req.RequestURI = req.URL.RequestURI()
		res, err := app.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()
		var result response
		require.NoError(t, json.NewDecoder(res.Body).Decode(&result))
		return result
	}
	original := get("?x%26all=true&x%3Dy=value&x%23z=hash&x%2Bz=plus&x%25z=percent&search=hello%26world&page[size]=2&page[after]=old-after&page[before]=old-before")
	require.False(t, original.All)
	for _, direction := range []struct {
		relation string
		key      string
		cursor   string
	}{
		{"prev", "page[before]", before},
		{"next", "page[after]", after},
	} {
		t.Run(direction.relation, func(t *testing.T) {
			require.NotNil(t, original.Links[direction.relation])
			followed := get(*original.Links[direction.relation])
			assert.False(t, followed.All)
			assert.Equal(t, map[string]string{
				"x&all": "true", "x=y": "value", "x#z": "hash", "x+z": "plus", "x%z": "percent",
				"search": "hello&world", "page[size]": "2", direction.key: direction.cursor,
			}, followed.Queries)
		})
	}
	assert.Equal(t, "old-after", original.Queries["page[after]"])
	assert.Equal(t, "old-before", original.Queries["page[before]"])
}
