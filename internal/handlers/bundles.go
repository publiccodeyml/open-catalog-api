package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/handlers/general"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

type BundleInterface interface {
	GetBundles(ctx *fiber.Ctx) error
	GetBundle(ctx *fiber.Ctx) error
	PostBundle(ctx *fiber.Ctx) error
	PatchBundle(ctx *fiber.Ctx) error
	DeleteBundle(ctx *fiber.Ctx) error
}

var errSoftwareNotFound = errors.New("one or more softwareIds do not exist")

type Bundle struct {
	db *gorm.DB
}

func NewBundle(db *gorm.DB) *Bundle {
	return &Bundle{db: db}
}

func (b *Bundle) GetBundles(ctx *fiber.Ctx) error {
	var bundles []models.Bundle

	stmt, err := general.Clauses(ctx, b.db, "")
	if err != nil {
		return common.Error(
			fiber.StatusUnprocessableEntity,
			"can't get Bundles",
			err.Error(),
		)
	}

	if all := ctx.QueryBool("all", false); !all {
		stmt = stmt.Scopes(models.Active)
	}

	stmt = stmt.Preload("Software")

	paginator, err := general.NewPaginator(ctx)
	if err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, "can't get Bundles", err.Error())
	}

	result, cursor, err := paginator.Paginate(stmt, &bundles)
	if err != nil {
		return common.Error(
			fiber.StatusUnprocessableEntity,
			"can't get Bundles",
			"wrong cursor format in page[after] or page[before]",
		)
	}

	if result.Error != nil {
		return common.Error(
			fiber.StatusInternalServerError,
			"can't get Bundles",
			fiber.ErrInternalServerError.Message,
		)
	}

	return ctx.JSON(fiber.Map{"data": &bundles, "links": general.NewPaginationLinks(ctx.Queries(), cursor)})
}

func (b *Bundle) GetBundle(ctx *fiber.Ctx) error {
	bundle := models.Bundle{}

	if err := b.db.Preload("Software").First(&bundle, "id = ?", ctx.Params("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, "can't get Bundle", "Bundle was not found")
		}

		return common.Error(
			fiber.StatusInternalServerError,
			"can't get Bundle",
			fiber.ErrInternalServerError.Message,
		)
	}

	return ctx.JSON(&bundle)
}

func (b *Bundle) PostBundle(ctx *fiber.Ctx) error {
	const errMsg = "can't create Bundle"

	request := new(common.BundlePost)

	if err := common.ValidateRequestEntity(ctx, request, errMsg); err != nil {
		return err //nolint:wrapcheck
	}

	software, err := findSoftwareByIDs(b.db, request.SoftwareIDs)
	if err != nil {
		return softwareLookupError(errMsg, err)
	}

	bundle := &models.Bundle{
		ID:          utils.UUIDv4(),
		Name:        request.Name,
		Description: request.Description,
		Active:      request.Active,
		Software:    software,
	}

	// Omit("Software.*") links the existing rows without upserting them.
	if err := b.db.Omit("Software.*").Create(bundle).Error; err != nil {
		return bundleSaveError(errMsg, err)
	}

	bundle.SoftwareIDs = request.SoftwareIDs

	return ctx.JSON(bundle)
}

func (b *Bundle) PatchBundle(ctx *fiber.Ctx) error {
	const errMsg = "can't update Bundle"

	bundle := models.Bundle{}

	if err := b.db.Preload("Software").First(&bundle, "id = ?", ctx.Params("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Bundle was not found")
		}

		return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
	}

	contentType := ctx.Get(fiber.HeaderContentType)
	if contentType != common.ContentTypeJSONPatch {
		if err := common.ValidateRequestEntity(ctx, new(common.BundlePatch), errMsg); err != nil {
			return err //nolint:wrapcheck
		}
	}

	updatedBundle, patchErr := common.ApplyPatch(&bundle, contentType, ctx.Body())
	if patchErr != nil {
		return common.Error(patchErr.Code, errMsg, patchErr.Error())
	}

	updatedBundle.ID = bundle.ID

	software, err := findSoftwareByIDs(b.db, updatedBundle.SoftwareIDs)
	if err != nil {
		return softwareLookupError(errMsg, err)
	}

	updatedBundle.Software = nil

	err = b.db.Transaction(func(tran *gorm.DB) error {
		if err := tran.Updates(&updatedBundle).Error; err != nil {
			return err
		}

		// Omit("Software.*") swaps the join rows without upserting the
		// software rows themselves.
		return tran.Model(&updatedBundle).Omit("Software.*").
			Association("Software").Replace(software)
	})
	if err != nil {
		return bundleSaveError(errMsg, err)
	}

	return ctx.JSON(&updatedBundle)
}

func (b *Bundle) DeleteBundle(ctx *fiber.Ctx) error {
	bundle := models.Bundle{}

	if err := b.db.First(&bundle, "id = ?", ctx.Params("id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, "can't delete Bundle", "Bundle was not found")
		}

		return common.Error(fiber.StatusInternalServerError, "can't delete Bundle", fiber.ErrInternalServerError.Message)
	}

	// Select("Software") also removes the bundles_software join rows.
	if err := b.db.Select("Software").Delete(&bundle).Error; err != nil {
		return common.Error(fiber.StatusInternalServerError, "can't delete Bundle", fiber.ErrInternalServerError.Message)
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}

// findSoftwareByIDs resolves ids to their software rows. Duplicate ids
// come back short and count as missing.
func findSoftwareByIDs(gormdb *gorm.DB, ids []string) ([]models.Software, error) {
	var software []models.Software

	if err := gormdb.Where("id IN ?", ids).Find(&software).Error; err != nil {
		return nil, err
	}

	if len(software) != len(ids) {
		return nil, errSoftwareNotFound
	}

	return software, nil
}

func softwareLookupError(errMsg string, err error) error {
	if errors.Is(err, errSoftwareNotFound) {
		return common.Error(fiber.StatusUnprocessableEntity, errMsg, err.Error())
	}

	return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
}

func bundleSaveError(errMsg string, err error) error {
	if field := common.DuplicateField(err); field != nil {
		detail := alreadyExists
		if *field != "" {
			detail = *field + " " + alreadyExists
		}

		return common.Error(fiber.StatusConflict, errMsg, detail)
	}

	return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
}
