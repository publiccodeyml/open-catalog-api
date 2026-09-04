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
	ID            string    `gorm:"primaryKey" json:"id"`
	AlternativeID *string   `json:"alternativeId"`
	Message       string    `json:"message"`
	Active        bool      `json:"active"`
	CreatedAt     time.Time `json:"createdAt"`
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

func TestPaginateFilterField(t *testing.T) {
	db := newCrudDB(t)
	seedCrudItems(t, db)

	items, _, err := paginate[crudItem](newCrudCtx(t, "filter=second"), db, listOptions{title: "t", filterField: "message"})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "two", items[0].ID)

	items, _, err = paginate[crudItem](newCrudCtx(t, "filter=second"), db, listOptions{title: "t"})
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestPaginateSearchField(t *testing.T) {
	db := newCrudDB(t)
	seedCrudItems(t, db)

	items, _, err := paginate[crudItem](newCrudCtx(t, "search=SEC"), db, listOptions{title: "t", searchField: "message"})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, "two", items[0].ID)

	// Without a search column the parameter is ignored, not sent to the
	// database as a reference to a column the table does not have.
	items, _, err = paginate[crudItem](newCrudCtx(t, "search=SEC"), db, listOptions{title: "t"})
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestPaginateDateWindow(t *testing.T) {
	db := newCrudDB(t)
	seedCrudItems(t, db)

	items, _, err := paginate[crudItem](
		newCrudCtx(t, "from=2024-01-01T00:30:00Z"), db, listOptions{title: "t", dateWindow: true},
	)
	require.NoError(t, err)
	assert.Len(t, items, 2)

	// Without the window the two parameters are ignored, a bad value
	// included: the list does not document them.
	items, _, err = paginate[crudItem](newCrudCtx(t, "from=2024-01-01T00:30:00Z"), db, listOptions{title: "t"})
	require.NoError(t, err)
	assert.Len(t, items, 3)

	items, _, err = paginate[crudItem](newCrudCtx(t, "from=not-a-date"), db, listOptions{title: "t"})
	require.NoError(t, err)
	assert.Len(t, items, 3)
}

func TestPaginateErrors(t *testing.T) {
	db := newCrudDB(t)
	seedCrudItems(t, db)

	_, _, err := paginate[crudItem](
		newCrudCtx(t, "from=not-a-date"), db, listOptions{title: "can't get Items", dateWindow: true},
	)
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

func TestFindOne(t *testing.T) {
	db := newCrudDB(t)
	seedCrudItems(t, db)

	item, err := findOne[crudItem](db, "one", findOptions{title: "can't get Item", name: "Item"})
	require.NoError(t, err)
	assert.Equal(t, "first", item.Message)

	_, err = findOne[crudItem](db, "alt-two", findOptions{title: "can't get Item", name: "Item"})
	assert.Equal(t, fiber.StatusNotFound, problemStatus(t, err))

	item, err = findOne[crudItem](db, "alt-two", findOptions{title: "can't get Item", name: "Item", byAlternativeID: true})
	require.NoError(t, err)
	assert.Equal(t, "two", item.ID)

	_, err = findOne[crudItem](db, "missing", findOptions{title: "can't get Item", name: "Item", byAlternativeID: true})
	require.Error(t, err)
	assert.Equal(t, fiber.StatusNotFound, problemStatus(t, err))
	assert.Equal(t, "Item was not found", err.(common.ProblemJSONError).Detail)
}

func TestWriteError(t *testing.T) {
	db := newCrudDB(t)
	seedCrudItems(t, db)

	err := writeError(idConflictError("one"), "can't create Item")
	problem, ok := errors.AsType[common.ProblemJSONError](err)
	require.True(t, ok)
	assert.Equal(t, fiber.StatusConflict, problem.Status)
	assert.Equal(t, "Publisher with id 'one' already exists", problem.Detail)

	dup := db.Create(&crudItem{ID: "one", Message: "again"}).Error
	require.Error(t, dup)

	err = writeError(dup, "can't create Item")
	problem, ok = errors.AsType[common.ProblemJSONError](err)
	require.True(t, ok)
	assert.Equal(t, fiber.StatusConflict, problem.Status)
	assert.Contains(t, problem.Detail, alreadyExists)

	err = writeError(errors.New("boom"), "can't create Item")
	problem, ok = errors.AsType[common.ProblemJSONError](err)
	require.True(t, ok)
	assert.Equal(t, fiber.StatusInternalServerError, problem.Status)
	assert.Equal(t, fiber.ErrInternalServerError.Message, problem.Detail)
}

// crudItemPatch stands in for the request DTOs: the merge patch path
// validates the body against it, the JSON Patch path skips it.
type crudItemPatch struct {
	Message *string `json:"message" validate:"omitempty,min=2"`
}

func newPatchCtx(t *testing.T, contentType, body string) *fiber.Ctx {
	t.Helper()

	ctx := newCrudCtx(t, "")
	ctx.Request().Header.SetContentType(contentType)
	ctx.Request().SetBodyString(body)

	return ctx
}

func validateCrudItem(item crudItem, title string) error {
	if len(item.Message) < 2 {
		return common.Error(fiber.StatusUnprocessableEntity, title, "message too short")
	}

	return nil
}

func TestApplyPatchMergePatch(t *testing.T) {
	current := crudItem{ID: "one", Message: "first", Active: true}
	restored := false

	opts := patchOptions[crudItem]{
		title:   "can't update Item",
		request: new(crudItemPatch),
		restore: func(updated, current *crudItem) {
			restored = true
			updated.ID = current.ID
		},
		validate: validateCrudItem,
	}

	updated, err := applyPatch(newPatchCtx(t, fiber.MIMEApplicationJSON, `{"message":"changed"}`), &current, opts)
	require.NoError(t, err)
	assert.Equal(t, "changed", updated.Message)
	assert.Equal(t, "one", updated.ID)
	assert.True(t, restored)

	_, err = applyPatch(newPatchCtx(t, fiber.MIMEApplicationJSON, `{"message":"x"}`), &current, opts)
	assert.Equal(t, fiber.StatusUnprocessableEntity, problemStatus(t, err))
	assert.NotEqual(t, "message too short", err.(common.ProblemJSONError).Detail)

	_, err = applyPatch(newPatchCtx(t, fiber.MIMEApplicationJSON, `{"message":null}`), &current, opts)
	assert.Equal(t, fiber.StatusUnprocessableEntity, problemStatus(t, err))
	assert.Equal(t, "message too short", err.(common.ProblemJSONError).Detail)

	_, err = applyPatch(newPatchCtx(t, fiber.MIMEApplicationJSON, `{"message":`), &current, opts)
	assert.Equal(t, fiber.StatusBadRequest, problemStatus(t, err))
}

func TestApplyPatchJSONPatch(t *testing.T) {
	current := crudItem{ID: "one", Message: "first", Active: true}

	opts := patchOptions[crudItem]{
		title:    "can't update Item",
		request:  new(crudItemPatch),
		restore:  func(updated, current *crudItem) { updated.ID = current.ID },
		validate: validateCrudItem,
	}

	updated, err := applyPatch(
		newPatchCtx(t, common.ContentTypeJSONPatch, `[{"op":"replace","path":"/message","value":"changed"}]`),
		&current, opts,
	)
	require.NoError(t, err)
	assert.Equal(t, "changed", updated.Message)

	_, err = applyPatch(
		newPatchCtx(t, common.ContentTypeJSONPatch, `[{"op":"replace","path":"/message","value":"x"}]`),
		&current, opts,
	)
	assert.Equal(t, fiber.StatusUnprocessableEntity, problemStatus(t, err))
	assert.Equal(t, "message too short", err.(common.ProblemJSONError).Detail)

	_, err = applyPatch(newPatchCtx(t, common.ContentTypeJSONPatch, `not a patch`), &current, opts)
	assert.Equal(t, fiber.StatusBadRequest, problemStatus(t, err))

	_, err = applyPatch(
		newPatchCtx(t, common.ContentTypeJSONPatch, `[{"op":"replace","path":"/id","value":"two"}]`),
		&current, opts,
	)
	assert.Equal(t, fiber.StatusUnprocessableEntity, problemStatus(t, err))
}
