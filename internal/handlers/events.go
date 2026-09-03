package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/pilagod/gorm-cursor-paginator/v2/paginator"
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
func (e *Event) GetEvents(ctx *fiber.Ctx) error {
	return list[models.Event](ctx, e.db, listOptions{
		title: "can't get Events",
		order: paginator.DESC,
	})
}

// GetEvent gets the event with the given ID and returns any error encountered.
func (e *Event) GetEvent(ctx *fiber.Ctx) error {
	event, err := findOne[models.Event](e.db, ctx.Params("id"), findOptions{title: "can't get Event", name: "Event"})
	if err != nil {
		return err
	}

	return ctx.JSON(event)
}
