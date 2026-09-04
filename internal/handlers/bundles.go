package handlers

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

var errSoftwareNotFound = errors.New("one or more softwareIds do not exist")

const (
	bundleEntityName    = "Bundle"
	softwareAssociation = "Software"
)

type Bundle struct {
	db *gorm.DB
}

func NewBundle(db *gorm.DB) *Bundle {
	return &Bundle{db: db}
}

func (b *Bundle) GetBundles(ctx *fiber.Ctx) error {
	return list[models.Bundle](ctx, b.db.Preload(softwareAssociation), listOptions{
		title:      "can't get Bundles",
		activeOnly: true,
	})
}

func (b *Bundle) GetBundle(ctx *fiber.Ctx) error {
	bundle, err := findOne[models.Bundle](b.db, ctx.Params("id"), findOptions{
		title:    "can't get Bundle",
		name:     bundleEntityName,
		preloads: []string{softwareAssociation},
	})
	if err != nil {
		return err
	}

	return ctx.JSON(bundle)
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
	if err := models.Transaction(writeDB(ctx, b.db), func(tran *gorm.DB) error {
		return tran.Omit("Software.*").Create(bundle).Error
	}); err != nil {
		return writeError(err, errMsg)
	}

	bundle.SoftwareIDs = request.SoftwareIDs

	return ctx.JSON(bundle)
}

func (b *Bundle) PatchBundle(ctx *fiber.Ctx) error {
	const errMsg = "can't update Bundle"

	bundle, err := findOne[models.Bundle](b.db, ctx.Params("id"), findOptions{
		title:    errMsg,
		name:     bundleEntityName,
		preloads: []string{softwareAssociation},
	})
	if err != nil {
		return err
	}

	updatedBundle, err := applyPatch(ctx, bundle, patchOptions[models.Bundle]{
		title:    errMsg,
		request:  new(common.BundlePatch),
		restore:  restoreBundle,
		validate: validatePatchedBundle,
	})
	if err != nil {
		return err
	}

	software, err := findSoftwareByIDs(b.db, updatedBundle.SoftwareIDs)
	if err != nil {
		return softwareLookupError(errMsg, err)
	}

	updatedBundle.Software = nil

	err = models.Transaction(writeDB(ctx, b.db), func(tran *gorm.DB) error {
		if err := updateColumns(tran, &updatedBundle, "Name", "Description", "Active"); err != nil {
			return err
		}

		// Omit("Software.*") swaps the join rows without upserting the
		// software rows themselves. The swap saves the bundle again, and
		// updateColumns has already recorded that update, so the hooks
		// are skipped here or the same change would emit two events.
		return tran.Session(&gorm.Session{SkipHooks: true}).Model(&updatedBundle).Omit("Software.*").
			Association("Software").Replace(software)
	})
	if err != nil {
		return writeError(err, errMsg)
	}

	return ctx.JSON(&updatedBundle)
}

func (b *Bundle) DeleteBundle(ctx *fiber.Ctx) error {
	const errMsg = "can't delete Bundle"

	bundle := models.Bundle{ID: ctx.Params("id")}

	// Select("Software") also removes the bundles_software join rows.
	if err := models.Transaction(writeDB(ctx, b.db), func(tran *gorm.DB) error {
		return deleteResult(tran.Select(softwareAssociation).Delete(&bundle))
	}); err != nil {
		return deleteError(err, errMsg, bundleEntityName)
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

	return common.InternalServerError(errMsg)
}
