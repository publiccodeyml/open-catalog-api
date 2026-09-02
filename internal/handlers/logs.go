package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/pilagod/gorm-cursor-paginator/v2/paginator"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

type LogInterface interface {
	GetLogs(ctx *fiber.Ctx) error
	GetLog(ctx *fiber.Ctx) error
	PostLog(ctx *fiber.Ctx) error
	PatchLog(ctx *fiber.Ctx) error
	DeleteLog(ctx *fiber.Ctx) error

	GetSoftwareLogs(ctx *fiber.Ctx) error
	PostSoftwareLog(ctx *fiber.Ctx) error

	PostCatalogLog(ctx *fiber.Ctx) error

	GetPublisherLogs(ctx *fiber.Ctx) error
	PostPublisherLog(ctx *fiber.Ctx) error
}

type Log struct {
	db *gorm.DB
}

func NewLog(db *gorm.DB) *Log {
	return &Log{db: db}
}

// GetLogs gets the list of all logs and returns any error encountered.
func (p *Log) GetLogs(ctx *fiber.Ctx) error {
	return list[models.Log](ctx, p.db, listOptions{
		title:       "can't get Logs",
		searchField: "message",
		order:       paginator.DESC,
	})
}

// GetLog gets the log with the given ID and returns any error encountered.
func (p *Log) GetLog(ctx *fiber.Ctx) error {
	log := models.Log{}

	if err := p.db.First(&log, "id = ?", ctx.Params("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, "can't get Log", "Log was not found")
		}

		return common.Error(
			fiber.StatusInternalServerError,
			"can't get Log",
			fiber.ErrInternalServerError.Message,
		)
	}

	return ctx.JSON(&log)
}

// PostLog creates a new log.
func (p *Log) PostLog(ctx *fiber.Ctx) error {
	const errMsg = "can't create Log"

	logReq := new(common.Log)

	if err := common.ValidateRequestEntity(ctx, logReq, errMsg); err != nil {
		return err //nolint:wrapcheck
	}

	log := models.Log{ID: utils.UUIDv4(), Message: logReq.Message}

	if err := p.db.Create(&log).Error; err != nil {
		return common.Error(fiber.StatusInternalServerError, errMsg, "db error")
	}

	return ctx.JSON(&log)
}

// PatchLog updates the log with the given ID.
func (p *Log) PatchLog(ctx *fiber.Ctx) error {
	const errMsg = "can't update Log"

	log := models.Log{}

	if err := p.db.First(&log, "id = ?", ctx.Params("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Log was not found")
		}

		return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
	}

	contentType := ctx.Get(fiber.HeaderContentType)
	if contentType != common.ContentTypeJSONPatch {
		if err := common.ValidateRequestEntity(ctx, &common.Log{}, errMsg); err != nil {
			return err //nolint:wrapcheck
		}
	}

	updatedLog, patchErr := common.ApplyPatch(&log, contentType, ctx.Body())
	if patchErr != nil {
		return common.Error(patchErr.Code, errMsg, patchErr.Error())
	}

	// Identity, timestamps and the entity association are immutable via this
	// endpoint, and ApplyPatch drops the json:"-" fields, so restore them.
	updatedLog.ID = log.ID
	updatedLog.CreatedAt = log.CreatedAt
	updatedLog.DeletedAt = log.DeletedAt
	updatedLog.EntityID = log.EntityID
	updatedLog.EntityType = log.EntityType

	if contentType == common.ContentTypeJSONPatch {
		if err := validatePatchedLog(updatedLog, errMsg); err != nil {
			return err
		}
	}

	if err := p.db.Updates(&updatedLog).Error; err != nil {
		return common.Error(fiber.StatusInternalServerError, errMsg, "db error")
	}

	return ctx.JSON(&updatedLog)
}

// DeleteLog deletes the log with the given ID.
func (p *Log) DeleteLog(ctx *fiber.Ctx) error {
	var log models.Log

	result := p.db.Delete(&log, "id = ?", ctx.Params("id"))

	if result.Error != nil {
		return common.Error(fiber.StatusInternalServerError, "can't delete Log", "db error")
	}

	if result.RowsAffected == 0 {
		return common.Error(fiber.StatusNotFound, "can't delete Log", "Log was not found")
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}

// GetSoftwareLogs gets the logs associated to a Software with the given ID and returns any error encountered.
func (p *Log) GetSoftwareLogs(ctx *fiber.Ctx) error {
	return p.getEntityLogs(ctx, &models.Software{}, "Software")
}

// PostCatalogLog creates a new log associated to a Catalog with the given ID and returns any error encountered.
func (p *Log) PostCatalogLog(ctx *fiber.Ctx) error {
	const errMsg = "can't create Log"

	logReq := new(common.Log)

	catalog, err := resolveCatalog(p.db, ctx.Params("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, "can't create Log", "Catalog was not found")
		}

		return common.Error(
			fiber.StatusInternalServerError,
			"can't get Catalog",
			fiber.ErrInternalServerError.Message,
		)
	}

	if err := common.ValidateRequestEntity(ctx, logReq, errMsg); err != nil {
		return err //nolint:wrapcheck
	}

	table := models.Catalog{}.TableName()

	var entityID *string
	if !isRoot(catalog) {
		entityID = &catalog.ID
	}

	log := models.Log{
		ID:         utils.UUIDv4(),
		Message:    logReq.Message,
		EntityID:   entityID,
		EntityType: &table,
	}

	if err := p.db.Create(&log).Error; err != nil {
		return common.Error(fiber.StatusInternalServerError, errMsg, "db error")
	}

	return ctx.JSON(&log)
}

// GetPublisherLogs gets the logs associated to a Publisher with the given ID and returns any error encountered.
func (p *Log) GetPublisherLogs(ctx *fiber.Ctx) error {
	return p.getEntityLogs(ctx, &models.Publisher{}, "Publisher")
}

// PostPublisherLog creates a new log associated to a Publisher with the given ID and returns any error encountered.
func (p *Log) PostPublisherLog(ctx *fiber.Ctx) error {
	return p.postEntityLog(ctx, &models.Publisher{}, "Publisher")
}

// PostSoftwareLog creates a new log associated to a Software with the given ID and returns any error encountered.
func (p *Log) PostSoftwareLog(ctx *fiber.Ctx) error {
	return p.postEntityLog(ctx, &models.Software{}, "Software")
}

// getEntityLogs gets the logs associated to the entity with the ID in the request path.
// entity must be a pointer, it is filled in with the row loaded from the database.
func (p *Log) getEntityLogs(ctx *fiber.Ctx, entity models.Model, entityName string) error {
	errMsg := "can't get " + entityName

	if err := p.db.First(entity, "id = ?", ctx.Params("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, entityName+" was not found")
		}

		return common.Error(
			fiber.StatusInternalServerError,
			errMsg,
			fiber.ErrInternalServerError.Message,
		)
	}

	stmt := p.db.
		Where(map[string]any{"entity_type": entity.TableName()}).
		Where("entity_id = ?", entity.UUID())

	return list[models.Log](ctx, stmt, listOptions{
		title:       "can't get Logs",
		order:       paginator.DESC,
		skipClauses: true,
	})
}

// postEntityLog creates a new log associated to the entity with the ID in the request path.
// entity must be a pointer, it is filled in with the row loaded from the database.
func (p *Log) postEntityLog(ctx *fiber.Ctx, entity models.Model, entityName string) error {
	const errMsg = "can't create Log"

	logReq := new(common.Log)

	if err := p.db.First(entity, "id = ?", ctx.Params("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, entityName+" was not found")
		}

		return common.Error(
			fiber.StatusInternalServerError,
			"can't get "+entityName,
			fiber.ErrInternalServerError.Message,
		)
	}

	if err := common.ValidateRequestEntity(ctx, logReq, errMsg); err != nil {
		return err //nolint:wrapcheck
	}

	entityID := entity.UUID()
	table := entity.TableName()

	log := models.Log{
		ID:         utils.UUIDv4(),
		Message:    logReq.Message,
		EntityID:   &entityID,
		EntityType: &table,
	}

	if err := p.db.Create(&log).Error; err != nil {
		return common.Error(fiber.StatusInternalServerError, errMsg, "db error")
	}

	return ctx.JSON(&log)
}
