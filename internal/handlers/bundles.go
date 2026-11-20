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
	return list[models.Bundle](ctx, b.db.Scopes(preloadSoftwareIDs), listOptions{
		title:      "can't get Bundles",
		activeOnly: true,
	})
}

func (b *Bundle) GetBundle(ctx *fiber.Ctx) error {
	bundle, err := findOne[models.Bundle](b.db, ctx.Params("id"), findOptions{
		title:  "can't get Bundle",
		name:   bundleEntityName,
		scopes: []func(*gorm.DB) *gorm.DB{preloadSoftwareIDs},
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

	return patchInTransaction(ctx, b.db, errMsg, func(tran *gorm.DB) (*models.Bundle, error) {
		bundle, err := findOne[models.Bundle](tran, ctx.Params("id"), findOptions{
			title:  errMsg,
			name:   bundleEntityName,
			scopes: []func(*gorm.DB) *gorm.DB{preloadSoftwareIDs},
			lock:   true,
		})
		if err != nil {
			return nil, err
		}

		return updateBundle(ctx, tran, *bundle)
	})
}

// updateBundle applies the PATCH body to bundle and saves it in tran,
// with its software. It runs under patchInTransaction, with bundle read
// after the lock.
func updateBundle(ctx *fiber.Ctx, tran *gorm.DB, bundle models.Bundle) (*models.Bundle, error) {
	const errMsg = "can't update Bundle"

	updatedBundle, err := applyPatch(ctx, &bundle, patchOptions[models.Bundle]{
		title:   errMsg,
		request: new(common.BundlePatch),
		restore: restoreBundle,
		validate: func(updated models.Bundle, title string) error {
			return validatePatchedBundle(updated, bundle, title)
		},
	})
	if err != nil {
		return nil, err
	}

	software, err := findSoftwareByIDs(tran, updatedBundle.SoftwareIDs)
	if err != nil {
		return nil, softwareLookupError(errMsg, err)
	}

	updatedBundle.Software = nil

	if err := updateColumns(tran, &updatedBundle, "Name", "Description", "Active"); err != nil {
		return nil, err
	}

	// Omit("Software.*") swaps the join rows without upserting the
	// software rows themselves. The swap saves the bundle again, and
	// updateColumns has already recorded that update, so the hooks
	// are skipped here or the same change would emit two events.
	err = tran.Session(&gorm.Session{SkipHooks: true}).Model(&updatedBundle).Omit("Software.*").
		Association("Software").Replace(software)
	if err != nil {
		return nil, err //nolint:wrapcheck // patchInTransaction maps the driver error to a status
	}

	return &updatedBundle, nil
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

// preloadSoftwareIDs loads only the id of each software in a bundle.
// Bundles answer just softwareIds, and the full rows carry publiccodeYml
// and vitality, so loading them would read megabytes per bundle.
func preloadSoftwareIDs(gormdb *gorm.DB) *gorm.DB {
	return gormdb.Preload(softwareAssociation, func(tx *gorm.DB) *gorm.DB {
		return tx.Select("id")
	})
}

// findSoftwareByIDs resolves ids to their software rows, with only the
// id loaded since the bundle links them without writing them. Request
// validation rejects duplicate ids, so a short result means a missing id.
func findSoftwareByIDs(gormdb *gorm.DB, ids []string) ([]models.Software, error) {
	var software []models.Software

	if err := gormdb.Select("id").Where("id IN ?", ids).Find(&software).Error; err != nil {
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
