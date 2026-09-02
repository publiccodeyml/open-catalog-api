package handlers

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/pilagod/gorm-cursor-paginator/v2/paginator"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// crudItem is the minimal shape the helpers need: the paginator keys
// (CreatedAt, ID), the Active scope column and an alternative id.
type crudItem struct {
	ID            string `gorm:"primaryKey"`
	AlternativeID *string
	Message       string
	Active        bool
	CreatedAt     time.Time
}

func newCrudDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "crud.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&crudItem{}))

	return db
}

func newCrudCtx(t *testing.T, query string) *fiber.Ctx {
	t.Helper()

	app := fiber.New()
	ctx := app.AcquireCtx(&fasthttp.RequestCtx{})
	ctx.Request().URI().SetQueryString(query)

	t.Cleanup(func() { app.ReleaseCtx(ctx) })

	return ctx
}

func seedCrudItems(t *testing.T, gormdb *gorm.DB) {
	t.Helper()

	base := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	alt := "alt-two"

	require.NoError(t, gormdb.Create(&[]crudItem{
		{ID: "one", Message: "first", Active: true, CreatedAt: base},
		{ID: "two", AlternativeID: &alt, Message: "second", Active: false, CreatedAt: base.Add(time.Hour)},
		{ID: "three", Message: "third", Active: true, CreatedAt: base.Add(2 * time.Hour)},
	}).Error)
}

func problemStatus(t *testing.T, err error) int {
	t.Helper()

	problem, ok := errors.AsType[common.ProblemJSONError](err)
	require.True(t, ok, "expected a ProblemJSONError, got %T", err)

	return problem.Status
}

func TestPaginateActiveOnlyHonoursAll(t *testing.T) {
	db := newCrudDB(t)
	seedCrudItems(t, db)

	items, _, err := paginate[crudItem](newCrudCtx(t, ""), db, listOptions{title: "t", activeOnly: true})
	require.NoError(t, err)
	assert.Len(t, items, 2)

	items, _, err = paginate[crudItem](newCrudCtx(t, "all=true"), db, listOptions{title: "t", activeOnly: true})
	require.NoError(t, err)
	assert.Len(t, items, 3)

	items, _, err = paginate[crudItem](newCrudCtx(t, ""), db, listOptions{title: "t"})
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestPaginateOrder(t *testing.T) {
	db := newCrudDB(t)
	seedCrudItems(t, db)

	items, _, err := paginate[crudItem](newCrudCtx(t, ""), db, listOptions{title: "t"})
	require.NoError(t, err)
	assert.Equal(t, "one", items[0].ID)

	items, _, err = paginate[crudItem](newCrudCtx(t, ""), db, listOptions{title: "t", order: paginator.DESC})
	require.NoError(t, err)
	assert.Equal(t, "three", items[0].ID)
}

func TestPaginateSearchField(t *testing.T) {
	db := newCrudDB(t)
	seedCrudItems(t, db)

	items, _, err := paginate[crudItem](newCrudCtx(t, "filter=second"), db, listOptions{title: "t", searchField: "message"})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "two", items[0].ID)

	items, _, err = paginate[crudItem](newCrudCtx(t, "filter=second"), db, listOptions{title: "t", skipClauses: true})
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestPaginateErrors(t *testing.T) {
	db := newCrudDB(t)
	seedCrudItems(t, db)

	_, _, err := paginate[crudItem](newCrudCtx(t, "from=not-a-date"), db, listOptions{title: "can't get Items"})
	assert.Equal(t, fiber.StatusUnprocessableEntity, problemStatus(t, err))

	_, _, err = paginate[crudItem](newCrudCtx(t, "page[after]=garbage"), db, listOptions{title: "can't get Items"})
	assert.Equal(t, fiber.StatusUnprocessableEntity, problemStatus(t, err))

	_, _, err = paginate[crudItem](newCrudCtx(t, "page[size]=abc"), db, listOptions{title: "can't get Items"})
	assert.Equal(t, fiber.StatusUnprocessableEntity, problemStatus(t, err))
}

func TestListWritesEnvelope(t *testing.T) {
	db := newCrudDB(t)
	seedCrudItems(t, db)

	ctx := newCrudCtx(t, "page[size]=2")
	require.NoError(t, list[crudItem](ctx, db, listOptions{title: "t"}))

	var body struct {
		Data  []crudItem         `json:"data"`
		Links map[string]*string `json:"links"`
	}

	require.NoError(t, json.Unmarshal(ctx.Response().Body(), &body))
	assert.Len(t, body.Data, 2)
	assert.NotNil(t, body.Links["next"])
}
