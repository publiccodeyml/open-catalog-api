package general

import (
	"errors"
	"log"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const unknownQueryDetail = "invalid query parameters"

// QueryErrorDetail turns an error raised while reading the query string into a
// problem detail. Only the errors listed here describe what the caller got
// wrong. Anything else comes from the paginator or the driver and can carry
// the schema or the failing statement, so it stays in the server log.
func QueryErrorDetail(err error) string {
	switch {
	case errors.Is(err, common.ErrInvalidDateTime):
		return common.ErrInvalidDateTime.Error()
	case errors.Is(err, errInvalidPageSize):
		return errInvalidPageSize.Error()
	case errors.Is(err, errPageSizeOutOfRange):
		return errPageSizeOutOfRange.Error()
	default:
		log.Printf("unexpected query parameter error: %s", err)

		return unknownQueryDetail
	}
}

// Clauses applies the ?filter, ?from, ?to and ?search query parameters
// to stmt. filterField is the column ?filter matches exactly and
// searchField the column ?search matches as a case insensitive
// substring, dateWindow turns ?from and ?to on. An empty column name or
// a false dateWindow leaves the parameter unread: a list must not act
// on a parameter its OpenAPI operation does not declare.
func Clauses(ctx *fiber.Ctx, stmt *gorm.DB, filterField, searchField string, dateWindow bool) (*gorm.DB, error) {
	ret := stmt

	if filterField != "" {
		filter := ctx.Query("filter", "")

		if filter != "" {
			ret = ret.Where(map[string]any{filterField: filter})
		}
	}

	if dateWindow {
		var err error

		ret, err = createdAtWindow(ctx, ret)
		if err != nil {
			return nil, err
		}
	}

	if searchField != "" {
		if search := ctx.Query("search", ""); search != "" {
			ret = ret.Where(clause.Expr{
				SQL:  "LOWER(?) LIKE ?",
				Vars: []any{clause.Column{Name: searchField}, "%" + utils.ToLower(search) + "%"},
			})
		}
	}

	return ret, nil
}

// createdAtWindow narrows stmt to the ?from and ?to window.
func createdAtWindow(ctx *fiber.Ctx, stmt *gorm.DB) (*gorm.DB, error) {
	ret := stmt

	if from := ctx.Query("from", ""); from != "" {
		at, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return nil, common.ErrInvalidDateTime
		}

		ret = ret.Where("created_at > ?", at)
	}

	if to := ctx.Query("to", ""); to != "" {
		at, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return nil, common.ErrInvalidDateTime
		}

		ret = ret.Where("created_at < ?", at)
	}

	return ret, nil
}
