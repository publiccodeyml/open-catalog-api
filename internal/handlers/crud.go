package handlers

import (
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/pilagod/gorm-cursor-paginator/v2/paginator"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/database"
	"github.com/publiccodeyml/open-catalog-api/internal/handlers/general"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// listOptions drives the query parameters shared by every paginated
// list: ?filter, ?from, ?to, ?search (through general.Clauses), ?all and
// the page cursor.
type listOptions struct {
	// title is the Problem JSON title on failure, e.g. "can't get Publishers".
	title string
	// filterField is the column matched by ?filter. Empty disables ?filter.
	filterField string
	// searchField is the column searched by ?search. Empty disables ?search.
	searchField string
	// order overrides the ASC default of the cursor paginator.
	order paginator.Order
	// activeOnly applies models.Active unless ?all=true is given.
	activeOnly bool
	// dateWindow narrows the list to the ?from and ?to window on
	// created_at. Only the lists whose OpenAPI operation declares the two
	// parameters read them, elsewhere they are ignored rather than
	// filtering a page or answering 422 on a value nobody documented.
	dateWindow bool
}

// paginate applies opts to stmt and returns one page of T plus the cursor
// for the pagination links. Errors are Problem JSON errors, ready to be
// returned from a handler as they are.
func paginate[T any](ctx *fiber.Ctx, stmt *gorm.DB, opts listOptions) ([]T, paginator.Cursor, error) {
	var items []T

	stmt, err := general.Clauses(ctx, stmt, opts.filterField, opts.searchField, opts.dateWindow)
	if err != nil {
		return nil, paginator.Cursor{}, common.Error(
			fiber.StatusUnprocessableEntity,
			opts.title,
			general.QueryErrorDetail(err),
		)
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
	// lock takes the write lock on the row before reading it, see lockRows.
	lock bool
}

// findOne loads the T with the given id. On a miss it returns a 404
// Problem JSON error, on any other database failure a 500 one, so a
// handler returns the error as it is.
//
// rawID is the escaped route parameter. With byAlternativeID it is
// decoded here exactly once, as in resolveCatalog, since an alternative
// id can hold a space or a non ASCII letter.
func findOne[T any](gormdb *gorm.DB, rawID string, opts findOptions) (*T, error) {
	conds := []any{"id = ?", rawID}
	if opts.byAlternativeID {
		id, err := url.PathUnescape(rawID)
		if err != nil {
			id = rawID
		}

		conds = []any{"id = ? OR alternative_id = ?", id, id}
	}

	if opts.lock {
		ids, err := lockRows[T](gormdb, conds...)
		if err != nil {
			return nil, common.InternalServerError(opts.title)
		}

		// Read the rows the lock took, not whatever conds match now.
		conds = []any{"id IN ?", ids}
		if len(ids) == 0 {
			return nil, common.Error(fiber.StatusNotFound, opts.title, opts.name+" was not found")
		}
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

// analysisOrEmpty answers a as the response body, or an empty AnalysisData
// when a is nil, so a bare {} never renders as the JSON literal null.
func analysisOrEmpty(a common.AnalysisData) common.AnalysisData {
	if a == nil {
		return common.AnalysisData{}
	}

	return a
}

// analysisOptions drives patchAnalysis.
type analysisOptions struct {
	// title is the Problem JSON title on failure, e.g. "can't update Software analysis".
	title string
	// name is the resource name in the 404 detail: "<name> was not found".
	name string
	// current answers the analysis the row holds. It runs only for an empty
	// patch: a handler that leaves the row to the merge, addressed by its
	// primary key, reads nothing on a request that writes.
	current func() (common.AnalysisData, error)
}

// patchAnalysis runs the PATCH sequence shared by the analysis endpoints:
// decode the body, stamp every namespace with its timestamp, answer the
// stored document when the patch is empty and merge it into model
// otherwise. Errors are Problem JSON errors, ready to be returned from a
// handler as they are.
func patchAnalysis(ctx *fiber.Ctx, gormdb *gorm.DB, model models.Analyzable, opts analysisOptions) error {
	if err := common.CheckJSONContentType(ctx, opts.title); err != nil {
		return err //nolint:wrapcheck
	}

	if err := common.CheckNULCharacters(ctx.Body(), opts.title); err != nil {
		return err //nolint:wrapcheck
	}

	var incoming common.AnalysisData
	if err := json.Unmarshal(ctx.Body(), &incoming); err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, opts.title, "invalid or malformed JSON")
	}

	patch, err := common.WithTimestamps(incoming, time.Now())
	if err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, opts.title, err.Error())
	}

	// An empty patch writes nothing: no updated_at bump, no event, no webhook.
	if len(patch) == 0 {
		current, err := opts.current()
		if err != nil {
			return err
		}

		return ctx.JSON(analysisOrEmpty(current))
	}

	merged, err := database.MergeAnalysis(writeDB(ctx, gormdb), model, patch)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, opts.title, opts.name+" was not found")
		}

		if tooLarge, ok := errors.AsType[common.AnalysisTooLargeError](err); ok {
			return common.Error(fiber.StatusUnprocessableEntity, opts.title, tooLarge.Error())
		}

		return common.InternalServerError(opts.title)
	}

	return ctx.JSON(merged)
}

// writeError maps the error of a create or update to Problem JSON: as it
// is when it already is one, 409 when a unique constraint or the
// alternativeId check failed, 500 otherwise. The raw database error never
// reaches the client.
func writeError(err error, title string) error {
	if problem, ok := errors.AsType[common.ProblemJSONError](err); ok {
		return problem
	}

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

// lockRows locks the rows matching conds as lockRow does and returns
// their primary keys, so the caller reads the rows it locked by them.
// Read again with conds, a PostgreSQL statement takes a new snapshot and
// can match another row: one that a concurrent PATCH gave the alternative
// id after the lock, which nothing locked. No row matching is not an
// error, the empty slice is the caller's 404.
func lockRows[T any](tran *gorm.DB, conds ...any) ([]string, error) {
	var locked []T

	err := tran.Model(&locked).
		Clauses(clause.Returning{Columns: []clause.Column{{Name: "id"}}}).
		Where(conds[0], conds[1:]...).
		UpdateColumn("updated_at", gorm.Expr("updated_at")).Error
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(locked))
	for i := range locked {
		ids = append(ids, reflect.ValueOf(&locked[i]).Elem().FieldByName("ID").String())
	}

	return ids, nil
}

// lockRow makes tran the only writer of the rows matching conds until it
// ends, so a PATCH reads the row no concurrent PATCH is about to overwrite.
// It writes the row back as it is, rather than selecting it FOR UPDATE:
// SQLite has no row locks, and a transaction that reads before writing
// fails with SQLITE_BUSY instead of waiting when another one writes first.
// A first write makes SQLite wait for the database write lock and
// PostgreSQL for the row lock. No row matching is not an error, the read
// that follows reports it. conds must name the primary key, which no
// concurrent write changes. Use lockRows when they match an alternative id.
func lockRow[T any](tran *gorm.DB, conds ...any) error {
	return tran.Model(new(T)).Where(conds[0], conds[1:]...).
		UpdateColumn("updated_at", gorm.Expr("updated_at")).Error
}

// lockParent locks the row with the given id in table, see lockRow, for a
// create that ties a new row to it, and answers 404 when it is gone. The
// delete of that row locks it first too, so one of them waits for the
// other: the create either sees the row deleted or commits before the
// delete removes the rows tied to it.
func lockParent(tran *gorm.DB, table, id, title, name string) error {
	result := tran.Table(table).Where("id = ?", id).
		UpdateColumn("updated_at", gorm.Expr("updated_at"))
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return common.Error(fiber.StatusNotFound, title, name+" was not found")
	}

	return nil
}

// patchInTransaction runs patch in one write transaction and answers the
// entity it returns. patch locks the row with lockRow, reads it and writes
// it back, so two concurrent PATCH requests on the same entity apply one
// after the other and neither restores the values the other replaced.
// Problem JSON errors from patch are returned as they are, any other error
// goes through writeError.
func patchInTransaction[T any](
	ctx *fiber.Ctx, gormdb *gorm.DB, title string, patch func(tran *gorm.DB) (*T, error),
) error {
	var updated *T

	if err := models.Transaction(writeDB(ctx, gormdb), func(tran *gorm.DB) error {
		var err error

		updated, err = patch(tran)

		return err
	}); err != nil {
		return writeError(err, title)
	}

	return ctx.JSON(updated)
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
	if !common.IsJSONPatch(contentType) {
		if err := common.ValidateRequestEntity(ctx, opts.request, opts.title); err != nil {
			return zero, err //nolint:wrapcheck
		}
	} else if err := common.CheckNULCharacters(ctx.Body(), opts.title); err != nil {
		return zero, err //nolint:wrapcheck
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

// deleteEntityLogs soft deletes the logs attached to entity, for the
// handlers deleting the entity itself.
func deleteEntityLogs(tran *gorm.DB, entity models.Model) error {
	return tran.
		Where("entity_type = ? AND entity_id = ?", entity.TableName(), entity.UUID()).
		Delete(&models.Log{}).Error
}

// deleteResult reports gorm.ErrRecordNotFound when the delete matched no
// row. gorm runs the AfterDelete hook either way, so a concurrent DELETE
// of an id already gone would otherwise answer 204 and record a second
// delete event.
func deleteResult(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}

	return nil
}

// deleteError maps the error of a delete to Problem JSON: 404 when the row
// was already gone, 500 otherwise.
func deleteError(err error, title, name string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return common.Error(fiber.StatusNotFound, title, name+" was not found")
	}

	return common.InternalServerError(title)
}

// updateColumns writes the listed columns of entity and only those. A plain
// struct update skips a nil pointer, so a merge patch nulling an optional
// field would answer 200 and leave the old value in the row. Naming the
// columns makes gorm write the NULL, and it still sets updated_at and still
// runs the model hooks.
func updateColumns[T any](tran *gorm.DB, entity *T, columns ...string) error {
	return tran.Model(entity).Select(columns).Updates(entity).Error
}
