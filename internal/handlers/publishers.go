package handlers

import (
	"fmt"
	"slices"
	"sort"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

const alreadyExists = "already exists"

type Publisher struct {
	db *gorm.DB
}

func NewPublisher(db *gorm.DB) *Publisher {
	return &Publisher{db: db}
}

// GetPublishers gets the list of all publishers and returns any error encountered.
func (p *Publisher) GetPublishers(ctx *fiber.Ctx) error {
	return list[models.Publisher](ctx, p.db.Preload(codeHostingAssociation), listOptions{
		title:      "can't get Publishers",
		activeOnly: true,
	})
}

// GetPublisher gets the publisher with the given ID and returns any error encountered.
func (p *Publisher) GetPublisher(ctx *fiber.Ctx) error {
	publisher, err := findOne[models.Publisher](p.db, ctx.Params("id"), findOptions{
		title:           "can't get Publisher",
		name:            publisherEntityName,
		byAlternativeID: true,
		preloads:        []string{codeHostingAssociation},
	})
	if err != nil {
		return err
	}

	return ctx.JSON(publisher)
}

// PostPublisher creates a new publisher.
func (p *Publisher) PostPublisher(ctx *fiber.Ctx) error {
	return createPublisher(ctx, writeDB(ctx, p.db), nil)
}

// PatchPublisher updates the publisher with the given ID.
// Supports both JSON Merge Patch (default) and JSON Patch (application/json-patch+json).
func (p *Publisher) PatchPublisher(ctx *fiber.Ctx) error {
	// Preload will load all the associated CodeHosting. We'll manually handle that later.
	found, err := findOne[models.Publisher](p.db, ctx.Params("id"), findOptions{
		title:           "can't update Publisher",
		name:            publisherEntityName,
		byAlternativeID: true,
		preloads:        []string{codeHostingAssociation},
	})
	if err != nil {
		return err
	}

	return updatePublisher(ctx, writeDB(ctx, p.db), *found)
}

// DeletePublisher deletes the publisher with the given ID.
func (p *Publisher) DeletePublisher(ctx *fiber.Ctx) error {
	found, err := findOne[models.Publisher](p.db, ctx.Params("id"), findOptions{
		title:           "can't delete Publisher",
		name:            publisherEntityName,
		byAlternativeID: true,
	})
	if err != nil {
		return err
	}

	publisher := *found

	if err := models.Transaction(writeDB(ctx, p.db), func(tran *gorm.DB) error {
		return tran.Select(codeHostingAssociation).Delete(&publisher).Error
	}); err != nil {
		return common.Error(fiber.StatusInternalServerError, "can't delete Publisher", "db error")
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}

// createPublisher creates a publisher from the request body. catalogID owns
// it, nil for the root catalog and for the catalog agnostic endpoint.
func createPublisher(ctx *fiber.Ctx, gormdb *gorm.DB, catalogID *string) error {
	const errMsg = "can't create Publisher"

	request := new(common.PublisherPost)

	if err := common.ValidateRequestEntity(ctx, request, errMsg); err != nil {
		return err //nolint:wrapcheck
	}

	publisher := &models.Publisher{
		ID:            utils.UUIDv4(),
		CatalogID:     catalogID,
		Description:   request.Description,
		Email:         common.NormalizeEmail(request.Email),
		Active:        request.Active,
		AlternativeID: request.AlternativeID,
	}

	for _, codeHost := range request.CodeHosting {
		publisher.CodeHosting = append(publisher.CodeHosting,
			models.CodeHosting{
				ID:    utils.UUIDv4(),
				URL:   common.NormalizeURL(codeHost.URL),
				Group: codeHost.Group,
			})
	}

	if err := models.Transaction(gormdb, func(tran *gorm.DB) error {
		if request.AlternativeID != nil {
			if err := checkAlternativeIDConflict(tran, *request.AlternativeID); err != nil {
				return err
			}
		}

		return tran.Create(&publisher).Error
	}); err != nil {
		return writeError(err, errMsg)
	}

	return ctx.JSON(publisher)
}

// updatePublisher applies the PATCH body to publisher and saves it, keeping
// its CodeHosting in sync.
func updatePublisher(ctx *fiber.Ctx, gormdb *gorm.DB, publisher models.Publisher) error {
	const errMsg = "can't update Publisher"

	updatedPublisher, err := applyPatch(ctx, &publisher, patchOptions[models.Publisher]{
		title:    errMsg,
		request:  new(common.PublisherPatch),
		restore:  restorePublisher,
		validate: validatePatchedPublisher,
	})
	if err != nil {
		return err
	}

	updatedPublisher.Email = common.NormalizeEmail(updatedPublisher.Email)

	expectedURLs := make([]string, 0, len(updatedPublisher.CodeHosting))
	for _, ch := range updatedPublisher.CodeHosting {
		expectedURLs = append(expectedURLs, common.NormalizeURL(ch.URL))
	}

	if err := models.Transaction(gormdb, func(tran *gorm.DB) error {
		if updatedPublisher.AlternativeID != nil &&
			(publisher.AlternativeID == nil || *updatedPublisher.AlternativeID != *publisher.AlternativeID) {
			if err := checkAlternativeIDConflict(tran, *updatedPublisher.AlternativeID); err != nil {
				return err
			}
		}

		codeHosting, err := syncCodeHosting(tran, publisher, expectedURLs)
		if err != nil {
			return err
		}

		publisher.Description = updatedPublisher.Description
		publisher.Email = updatedPublisher.Email
		publisher.Active = updatedPublisher.Active
		publisher.AlternativeID = updatedPublisher.AlternativeID

		// Set CodeHosting to a zero value, so it's not touched by gorm's Update(),
		// because we handle it manually via syncCodeHosting.
		publisher.CodeHosting = []models.CodeHosting{}

		err = updateColumns(tran, &publisher, "Description", "Email", "Active", "AlternativeID")
		if err != nil {
			return err
		}

		publisher.CodeHosting = codeHosting

		return nil
	}); err != nil {
		return writeError(err, errMsg)
	}

	// Sort codeHosting to always have a consistent output.
	sort.Slice(publisher.CodeHosting, func(a int, b int) bool {
		return publisher.CodeHosting[a].URL < publisher.CodeHosting[b].URL
	})

	return ctx.JSON(&publisher)
}

// idConflictError is returned when alternativeId conflicts with an existing publisher's primary key.
type idConflictError string

func (e idConflictError) Error() string {
	return fmt.Sprintf("Publisher with id '%s' already exists", string(e))
}

// checkAlternativeIDConflict returns idConflictError if any publisher exists whose primary key
// equals the given alternativeID value, which would cause ambiguous lookups.
func checkAlternativeIDConflict(db *gorm.DB, alternativeID string) error {
	result := db.Limit(1).Find(&models.Publisher{ID: alternativeID})
	if result.Error != nil {
		return result.Error
	}

	if result.RowsAffected != 0 {
		return idConflictError(alternativeID)
	}

	return nil
}

// syncCodeHosting synchs the CodeHosting for a `publisher` in the database to reflect the
// passed slice of `codeHosting` URLs.
//
// It returns the slice of CodeHosting in the database.
func syncCodeHosting( //nolint:cyclop // mostly error handling ifs
	gormdb *gorm.DB, publisher models.Publisher, codeHosting []string,
) ([]models.CodeHosting, error) {
	toRemove := []string{}          // Slice of CodeHosting ids to remove from the database
	toAdd := []models.CodeHosting{} // Slice of CodeHosting to add to the database

	// Map mirroring the state of CodeHosting for this software in the database,
	// keyed by url
	urlMap := map[string]models.CodeHosting{}

	for _, ch := range publisher.CodeHosting {
		urlMap[ch.URL] = ch
	}

	for url, ch := range urlMap {
		if !slices.Contains(codeHosting, url) {
			toRemove = append(toRemove, ch.ID)

			delete(urlMap, url)
		}
	}

	for _, url := range codeHosting {
		_, exists := urlMap[url]
		if !exists {
			ch := models.CodeHosting{ID: utils.UUIDv4(), URL: url, PublisherID: publisher.ID}

			toAdd = append(toAdd, ch)
			urlMap[url] = ch
		}
	}

	if len(toRemove) > 0 {
		if err := gormdb.Delete(&models.CodeHosting{}, toRemove).Error; err != nil {
			return nil, err
		}
	}

	if len(toAdd) > 0 {
		if err := gormdb.Create(toAdd).Error; err != nil {
			return nil, err
		}
	}

	retCodeHosting := make([]models.CodeHosting, 0, len(urlMap))
	for _, ch := range urlMap {
		retCodeHosting = append(retCodeHosting, ch)
	}

	return retCodeHosting, nil
}
