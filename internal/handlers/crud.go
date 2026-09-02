package handlers

import (
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
