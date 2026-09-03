package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/pilagod/gorm-cursor-paginator/v2/paginator"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/handlers/general"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

// listOptions drives the query parameters shared by every paginated
// list: ?filter, ?from, ?to, ?search (through general.Clauses), ?all and
// the page cursor.
type listOptions struct {
	// title is the Problem JSON title on failure, e.g. "can't get Publishers".
	title string
	// searchField is the column matched by ?filter. Empty disables ?filter.
	searchField string
	// order overrides the ASC default of the cursor paginator.
	order paginator.Order
	// activeOnly applies models.Active unless ?all=true is given.
	activeOnly bool
	// skipClauses leaves ?filter, ?from, ?to and ?search untouched,
	// either because the list never supported them (webhooks, per
	// resource logs) or because the handler applied them itself before
	// a short circuit (the software lists).
	skipClauses bool
}

// paginate applies opts to stmt and returns one page of T plus the cursor
// for the pagination links. Errors are Problem JSON errors, ready to be
// returned from a handler as they are.
func paginate[T any](ctx *fiber.Ctx, stmt *gorm.DB, opts listOptions) ([]T, paginator.Cursor, error) {
	var items []T

	if !opts.skipClauses {
		var err error

		stmt, err = general.Clauses(ctx, stmt, opts.searchField)
		if err != nil {
			return nil, paginator.Cursor{}, common.Error(
				fiber.StatusUnprocessableEntity,
				opts.title,
				general.QueryErrorDetail(err),
			)
		}
	}

	if opts.activeOnly && !ctx.QueryBool("all", false) {
		stmt = stmt.Scopes(models.Active)
	}

	// An unset order leaves the default ASC of DefaultConfig in place.
	pager, err := general.NewPaginatorWithConfig(ctx, &paginator.Config{Order: opts.order})
	if err != nil {
		return nil, paginator.Cursor{}, common.Error(
			fiber.StatusUnprocessableEntity,
			opts.title,
			general.QueryErrorDetail(err),
		)
	}

	result, cursor, err := pager.Paginate(stmt, &items)
	if err != nil {
		return nil, paginator.Cursor{}, common.Error(
			fiber.StatusUnprocessableEntity,
			opts.title,
			"wrong cursor format in page[after] or page[before]",
		)
	}

	if result.Error != nil {
		return nil, paginator.Cursor{}, common.InternalServerError(opts.title)
	}

	return items, cursor, nil
}

// listJSON writes the list envelope every collection endpoint returns:
// {"data": [...], "links": {...}}.
func listJSON[T any](ctx *fiber.Ctx, items []T, cursor paginator.Cursor) error {
	return ctx.JSON(fiber.Map{"data": &items, "links": general.NewPaginationLinks(ctx.Queries(), cursor)})
}

// list is paginate followed by listJSON, for the handlers that have
// nothing to do in between.
func list[T any](ctx *fiber.Ctx, stmt *gorm.DB, opts listOptions) error {
	items, cursor, err := paginate[T](ctx, stmt, opts)
	if err != nil {
		return err
	}

	return listJSON(ctx, items, cursor)
}

const (
	publisherEntityName    = "Publisher"
	codeHostingAssociation = "CodeHosting"
)

// writeDB is how the actor reaches the hooks that record an event: the
// statement context is the only context they get to look at.
func writeDB(ctx *fiber.Ctx, gormdb *gorm.DB) *gorm.DB {
	return gormdb.WithContext(ctx.UserContext())
}

// findOptions drives findOne.
type findOptions struct {
	// title is the Problem JSON title on failure, e.g. "can't get Publisher".
	title string
	// name is the resource name in the 404 detail: "<name> was not found".
	name string
	// byAlternativeID also matches the alternative_id column.
	byAlternativeID bool
	preloads        []string
}

// findOne loads the T with the given id. On a miss it returns a 404
// Problem JSON error, on any other database failure a 500 one, so a
// handler returns the error as it is.
func findOne[T any](gormdb *gorm.DB, id string, opts findOptions) (*T, error) {
	conds := []any{"id = ?", id}
	if opts.byAlternativeID {
		conds = []any{"id = ? OR alternative_id = ?", id, id}
	}

	var item T

	stmt := gormdb
	for _, preload := range opts.preloads {
		stmt = stmt.Preload(preload)
	}

	if err := stmt.First(&item, conds...).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, common.Error(fiber.StatusNotFound, opts.title, opts.name+" was not found")
		}

		return nil, common.InternalServerError(opts.title)
	}

	return &item, nil
}

// writeError maps the error of a create or update to Problem JSON: 409
// when a unique constraint or the alternativeId check failed, 500
// otherwise. The raw database error never reaches the client.
func writeError(err error, title string) error {
	if idConflict, ok := errors.AsType[idConflictError](err); ok {
		return common.Error(fiber.StatusConflict, title, idConflict.Error())
	}

	if field := common.DuplicateField(err); field != nil {
		detail := alreadyExists
		if *field != "" {
			detail = *field + " " + alreadyExists
		}

		return common.Error(fiber.StatusConflict, title, detail)
	}

	return common.InternalServerError(title)
}

// patchOptions drives applyPatch.
type patchOptions[T any] struct {
	// title is the Problem JSON title on failure, e.g. "can't update Bundle".
	title string
	// request is a fresh request DTO, e.g. new(common.BundlePatch). A merge
	// patch body is validated against it before being applied.
	request any
	// restore copies the server owned fields (id, createdAt, the scoping
	// key) from current into updated. ApplyPatch already refuses an
	// operation on them, this keeps the save correct should one slip past.
	restore func(updated, current *T)
	// validate checks the entity the patch produced. A JSON Patch reaches
	// the entity fields directly, and a merge patch can null a field the
	// entity requires: the DTO decodes null and absent alike, to a nil
	// pointer, so only the outcome tells them apart.
	validate func(T, string) error
}

// applyPatch runs the PATCH sequence shared by every resource: validate a
// merge patch body, apply the patch, restore the server owned fields and
// validate the outcome. Errors are Problem JSON errors, ready to be
// returned from a handler as they are.
func applyPatch[T any](ctx *fiber.Ctx, current *T, opts patchOptions[T]) (T, error) { //nolint:ireturn
	var zero T

	contentType := ctx.Get(fiber.HeaderContentType)
	if contentType != common.ContentTypeJSONPatch {
		if err := common.ValidateRequestEntity(ctx, opts.request, opts.title); err != nil {
			return zero, err //nolint:wrapcheck
		}
	}

	updated, patchErr := common.ApplyPatch(current, contentType, ctx.Body())
	if patchErr != nil {
		return zero, common.Error(patchErr.Code, opts.title, patchErr.Error())
	}

	opts.restore(&updated, current)

	if err := opts.validate(updated, opts.title); err != nil {
		return zero, err
	}

	return updated, nil
}

// updateColumns writes the listed columns of entity and only those. A plain
// struct update skips a nil pointer, so a merge patch nulling an optional
// field would answer 200 and leave the old value in the row. Naming the
// columns makes gorm write the NULL, and it still sets updated_at and still
// runs the model hooks.
func updateColumns[T any](tran *gorm.DB, entity *T, columns ...string) error {
	return tran.Model(entity).Select(columns).Updates(entity).Error
}
