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

type Log struct {
	db *gorm.DB
	// searches holds a token for each ?search list running, nil when
	// the pool has no limit. See searchSlots.
	searches chan struct{}
}

func NewLog(db *gorm.DB) *Log {
	return &Log{db: db, searches: searchSlots(db)}
}

// searchSlots bounds the ?search lists running at once to half the
// connection pool, at least one. Anyone can search the logs, and a
// substring search the index can't narrow, like a term with fewer than
// three letters, reads every log: without a bound a stream of them
// takes every connection and every other request waits.
func searchSlots(db *gorm.DB) chan struct{} {
	pool, err := db.DB()
	if err != nil {
		return nil
	}

	size := pool.Stats().MaxOpenConnections
	if size == 0 {
		return nil
	}

	return make(chan struct{}, max(1, size/2))
}

// GetLogs gets the list of all logs and returns any error encountered.
func (p *Log) GetLogs(ctx *fiber.Ctx) error {
	const errMsg = "can't get Logs"

	if p.searches != nil && ctx.Query("search", "") != "" {
		select {
		case p.searches <- struct{}{}:
			defer func() { <-p.searches }()
		default:
			ctx.Set(fiber.HeaderRetryAfter, "1")

			return common.Error(fiber.StatusServiceUnavailable, errMsg, "too many log searches are running")
		}
	}

	return list[models.Log](ctx, p.db, listOptions{
		title:       errMsg,
		filterField: "message",
		searchField: "message",
		order:       paginator.DESC,
		dateWindow:  true,
	})
}

// GetLog gets the log with the given ID and returns any error encountered.
func (p *Log) GetLog(ctx *fiber.Ctx) error {
	log, err := findOne[models.Log](p.db, ctx.Params("id"), findOptions{title: "can't get Log", name: "Log"})
	if err != nil {
		return err
	}

	return ctx.JSON(log)
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
		return common.InternalServerError(errMsg)
	}

	return ctx.JSON(&log)
}

// PatchLog updates the log with the given ID.
func (p *Log) PatchLog(ctx *fiber.Ctx) error {
	const errMsg = "can't update Log"

	return patchInTransaction(ctx, p.db, errMsg, func(tran *gorm.DB) (*models.Log, error) {
		log, err := findOne[models.Log](tran, ctx.Params("id"), findOptions{title: errMsg, name: "Log", lock: true})
		if err != nil {
			return nil, err
		}

		updatedLog, err := applyPatch(ctx, log, patchOptions[models.Log]{
			title:    errMsg,
			request:  &common.Log{},
			restore:  restoreLog,
			validate: validatePatchedLog,
		})
		if err != nil {
			return nil, err
		}

		if err := tran.Updates(&updatedLog).Error; err != nil {
			return nil, err
		}

		return &updatedLog, nil
	})
}

// DeleteLog deletes the log with the given ID.
func (p *Log) DeleteLog(ctx *fiber.Ctx) error {
	var log models.Log

	result := p.db.Delete(&log, "id = ?", ctx.Params("id"))

	if result.Error != nil {
		return common.InternalServerError("can't delete Log")
	}

	if result.RowsAffected == 0 {
		return common.Error(fiber.StatusNotFound, "can't delete Log", "Log was not found")
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}

// GetSoftwareLogs gets the logs associated to a Software with the given ID and returns any error encountered.
func (p *Log) GetSoftwareLogs(ctx *fiber.Ctx) error {
	return p.getEntityLogs(ctx, &models.Software{}, softwareEntityName)
}

// PostCatalogLog creates a new log associated to a Catalog with the given ID and returns any error encountered.
func (p *Log) PostCatalogLog(ctx *fiber.Ctx) error {
	const errMsg = "can't create Log"

	logReq := new(common.Log)

	catalog, err := resolveCatalogOr404(p.db, ctx.Params("id"), errMsg)
	if err != nil {
		return err
	}

	if err := common.ValidateRequestEntity(ctx, logReq, errMsg); err != nil {
		return err //nolint:wrapcheck
	}

	table := models.Catalog{}.TableName()
	entityID := catalogOwnerID(catalog)

	log := models.Log{
		ID:         utils.UUIDv4(),
		Message:    logReq.Message,
		EntityID:   entityID,
		EntityType: &table,
	}

	if err := models.Transaction(p.db, func(tran *gorm.DB) error {
		if err := lockOwnerCatalog(tran, entityID, errMsg); err != nil {
			return err
		}

		return tran.Create(&log).Error
	}); err != nil {
		return writeError(err, errMsg)
	}

	return ctx.JSON(&log)
}

// GetPublisherLogs gets the logs associated to a Publisher with the given ID and returns any error encountered.
func (p *Log) GetPublisherLogs(ctx *fiber.Ctx) error {
	return p.getEntityLogs(ctx, &models.Publisher{}, publisherEntityName)
}

// PostPublisherLog creates a new log associated to a Publisher with the given ID and returns any error encountered.
func (p *Log) PostPublisherLog(ctx *fiber.Ctx) error {
	return p.postEntityLog(ctx, &models.Publisher{}, publisherEntityName)
}

// PostSoftwareLog creates a new log associated to a Software with the given ID and returns any error encountered.
func (p *Log) PostSoftwareLog(ctx *fiber.Ctx) error {
	return p.postEntityLog(ctx, &models.Software{}, softwareEntityName)
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
		title:      "can't get Logs",
		order:      paginator.DESC,
		dateWindow: true,
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

	if err := models.Transaction(p.db, func(tran *gorm.DB) error {
		if err := lockParent(tran, table, entityID, errMsg, entityName); err != nil {
			return err
		}

		return tran.Create(&log).Error
	}); err != nil {
		return writeError(err, errMsg)
	}

	return ctx.JSON(&log)
}
