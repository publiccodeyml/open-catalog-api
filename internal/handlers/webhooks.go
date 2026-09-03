package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

type Webhook[T models.Model] struct {
	db *gorm.DB
}

func NewWebhook[T models.Model](db *gorm.DB) *Webhook[T] {
	return &Webhook[T]{db: db}
}

func webhookFormatOrDefault(format string) string {
	if format == "" {
		return common.WebhookFormatDefault
	}

	return format
}

// GetWebhook gets the webhook with the given ID and returns any error encountered.
func (p *Webhook[T]) GetWebhook(ctx *fiber.Ctx) error {
	webhook, err := findOne[models.Webhook](p.db, ctx.Params("id"), findOptions{
		title: "can't get Webhook",
		name:  "Webhook",
	})
	if err != nil {
		return err
	}

	return ctx.JSON(webhook)
}

// GetResourceWebhooks gets the webhooks associated to resources
// (fe. Software, Publishers) and returns any error encountered.
func (p *Webhook[T]) GetResourceWebhooks(ctx *fiber.Ctx) error {
	var resource T

	stmt := p.db.Where(map[string]any{"entity_type": resource.TableName()})

	return list[models.Webhook](ctx, stmt, listOptions{title: "can't get Webhooks", skipClauses: true})
}

// GetSingleResourceWebhooks gets the webhooks associated to a resource
// (fe. a specific Software or Publisher) with the given ID and returns any
// error encountered.
func (p *Webhook[T]) GetSingleResourceWebhooks(ctx *fiber.Ctx) error {
	found, err := findOne[T](p.db, ctx.Params("id"), findOptions{
		title: "can't find resource",
		name:  "resource",
	})
	if err != nil {
		return err
	}

	resource := *found

	stmt := p.db.
		Where(map[string]any{"entity_type": resource.TableName()}).
		Where("entity_id = ?", resource.UUID())

	return list[models.Webhook](ctx, stmt, listOptions{title: "can't get Webhooks", skipClauses: true})
}

// PostResourceWebhook creates a new webhook associated to resources
// (fe. Software, Publishers) and returns any error encountered.
func (p *Webhook[T]) PostResourceWebhook(ctx *fiber.Ctx) error {
	const errMsg = "can't create Webhook"

	webhookReq := new(common.Webhook)

	var resource T

	if err := common.ValidateRequestEntity(ctx, webhookReq, errMsg); err != nil {
		return err //nolint:wrapcheck
	}

	webhook := models.Webhook{
		ID:         utils.UUIDv4(),
		URL:        common.NormalizeURL(webhookReq.URL),
		Format:     webhookFormatOrDefault(webhookReq.Format),
		Secret:     webhookReq.Secret,
		EntityID:   "", // this webhook is triggered for all the resources of this kind
		EntityType: resource.TableName(),
	}

	if err := models.Transaction(writeDB(ctx, p.db), func(tran *gorm.DB) error {
		return tran.Create(&webhook).Error
	}); err != nil {
		return common.Error(fiber.StatusInternalServerError, errMsg, "db error")
	}

	return ctx.JSON(&webhook)
}

// PostSingleResourceWebhook creates a new webhook associated to a resource with the given ID
// (fe. a specific Software or Publisher) and returns any error encountered.
func (p *Webhook[T]) PostSingleResourceWebhook(ctx *fiber.Ctx) error {
	const errMsg = "can't create Webhook"

	webhookReq := new(common.Webhook)

	found, err := findOne[T](p.db, ctx.Params("id"), findOptions{
		title: "can't find resource",
		name:  "resource",
	})
	if err != nil {
		return err
	}

	resource := *found

	if err := common.ValidateRequestEntity(ctx, webhookReq, errMsg); err != nil {
		return err //nolint:wrapcheck
	}

	webhook := models.Webhook{
		ID:         utils.UUIDv4(),
		URL:        common.NormalizeURL(webhookReq.URL),
		Format:     webhookFormatOrDefault(webhookReq.Format),
		Secret:     webhookReq.Secret,
		EntityID:   resource.UUID(),
		EntityType: resource.TableName(),
	}

	if err := models.Transaction(writeDB(ctx, p.db), func(tran *gorm.DB) error {
		return tran.Create(&webhook).Error
	}); err != nil {
		return common.Error(fiber.StatusInternalServerError, errMsg, "db error")
	}

	return ctx.JSON(&webhook)
}

// DeleteWebhook deletes the webhook with the given ID. The row is looked
// up first, so that the AfterDelete hook records no event for a missing
// id.
func (p *Webhook[T]) DeleteWebhook(ctx *fiber.Ctx) error {
	const errMsg = "can't delete Webhook"

	webhook, err := findOne[models.Webhook](p.db, ctx.Params("id"), findOptions{title: errMsg, name: "Webhook"})
	if err != nil {
		return err
	}

	if err := models.Transaction(writeDB(ctx, p.db), func(tran *gorm.DB) error {
		return tran.Delete(webhook).Error
	}); err != nil {
		return common.Error(fiber.StatusInternalServerError, errMsg, "db error")
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}
