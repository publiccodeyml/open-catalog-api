package handlers

import (
	"slices"

	"github.com/gofiber/fiber/v2"
	"github.com/pilagod/gorm-cursor-paginator/v2/paginator"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

type Event struct {
	db *gorm.DB
}

func NewEvent(db *gorm.DB) *Event {
	return &Event{db: db}
}

// GetEvents gets the list of all events and returns any error encountered.
// ?entityType, ?entityId, ?type and ?actor each narrow the list to an
// exact match, so the trail of one entity or one actor can be read on
// its own.
func (e *Event) GetEvents(ctx *fiber.Ctx) error {
	const errMsg = "can't get Events"

	stmt := e.db

	for param, column := range map[string]string{
		"entityType": "entity_type",
		"entityId":   "entity_id",
		"actor":      "actor",
	} {
		if value := ctx.Query(param); value != "" {
			stmt = stmt.Where(map[string]any{column: value})
		}
	}

	if eventType := ctx.Query("type"); eventType != "" {
		known := []string{common.EventTypeCreate, common.EventTypeUpdate, common.EventTypeDelete}
		if !slices.Contains(known, eventType) {
			return common.Error(fiber.StatusUnprocessableEntity, errMsg, "type must be one of create, update, delete")
		}

		stmt = stmt.Where(map[string]any{"type": eventType})
	}

	return list[models.Event](ctx, stmt, listOptions{title: errMsg, order: paginator.DESC, dateWindow: true})
}

// GetEvent gets the event with the given ID and returns any error encountered.
func (e *Event) GetEvent(ctx *fiber.Ctx) error {
	event, err := findOne[models.Event](e.db, ctx.Params("id"), findOptions{title: "can't get Event", name: "Event"})
	if err != nil {
		return err
	}

	return ctx.JSON(event)
}
