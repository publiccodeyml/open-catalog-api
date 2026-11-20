package handlers

import (
	"fmt"
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
		dateWindow: true,
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
	const errMsg = "can't update Publisher"

	return patchInTransaction(ctx, p.db, errMsg, func(tran *gorm.DB) (*models.Publisher, error) {
		// Preload will load all the associated CodeHosting. We'll manually handle that later.
		found, err := findOne[models.Publisher](tran, ctx.Params("id"), findOptions{
			title:           errMsg,
			name:            publisherEntityName,
			byAlternativeID: true,
			preloads:        []string{codeHostingAssociation},
			lock:            true,
		})
		if err != nil {
			return nil, err
		}

		return updatePublisher(ctx, tran, *found)
	})
}

// DeletePublisher deletes the publisher with the given ID. The lookup
// stays because the delete matches the UUID alone, while the id in the
// path can be an alternative id.
func (p *Publisher) DeletePublisher(ctx *fiber.Ctx) error {
	const errMsg = "can't delete Publisher"

	found, err := findOne[models.Publisher](p.db, ctx.Params("id"), findOptions{
		title:           errMsg,
		name:            publisherEntityName,
		byAlternativeID: true,
	})
	if err != nil {
		return err
	}

	publisher := *found

	if err := models.Transaction(writeDB(ctx, p.db), func(tran *gorm.DB) error {
		if err := deleteEntityLogs(tran, publisher); err != nil {
			return err
		}

		return deleteResult(tran.Select(codeHostingAssociation).Delete(&publisher))
	}); err != nil {
		return deleteError(err, errMsg, publisherEntityName)
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

	seenURLs := make(map[string]struct{}, len(request.CodeHosting))
	for _, codeHost := range request.CodeHosting {
		url := common.NormalizeURL(codeHost.URL)
		if _, exists := seenURLs[url]; exists {
			return common.Error(fiber.StatusUnprocessableEntity, errMsg,
				fmt.Sprintf("codeHosting url %s repeats another entry", codeHost.URL))
		}

		seenURLs[url] = struct{}{}

		publisher.CodeHosting = append(publisher.CodeHosting,
			models.CodeHosting{
				ID:    utils.UUIDv4(),
				URL:   url,
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

// updatePublisher applies the PATCH body to publisher and saves it in
// tran, keeping its CodeHosting in sync. It runs under patchInTransaction,
// with publisher read after the lock.
func updatePublisher(ctx *fiber.Ctx, tran *gorm.DB, publisher models.Publisher) (*models.Publisher, error) {
	updatedPublisher, err := applyPatch(ctx, &publisher, patchOptions[models.Publisher]{
		title:    "can't update Publisher",
		request:  new(common.PublisherPatch),
		restore:  restorePublisher,
		validate: validatePatchedPublisher,
	})
	if err != nil {
		return nil, err
	}

	updatedPublisher.Email = common.NormalizeEmail(updatedPublisher.Email)

	expectedCodeHosting := make([]models.CodeHosting, 0, len(updatedPublisher.CodeHosting))
	for _, ch := range updatedPublisher.CodeHosting {
		expectedCodeHosting = append(expectedCodeHosting,
			models.CodeHosting{URL: common.NormalizeURL(ch.URL), Group: ch.Group})
	}

	if updatedPublisher.AlternativeID != nil &&
		(publisher.AlternativeID == nil || *updatedPublisher.AlternativeID != *publisher.AlternativeID) {
		if err := checkAlternativeIDConflict(tran, *updatedPublisher.AlternativeID); err != nil {
			return nil, err
		}
	}

	codeHosting, err := syncCodeHosting(tran, publisher, expectedCodeHosting)
	if err != nil {
		return nil, err
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
		return nil, err
	}

	publisher.CodeHosting = codeHosting

	// Sort codeHosting to always have a consistent output.
	sort.Slice(publisher.CodeHosting, func(a int, b int) bool {
		return publisher.CodeHosting[a].URL < publisher.CodeHosting[b].URL
	})

	return &publisher, nil
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
// passed `codeHosting` entries, matched by URL.
//
// It returns the slice of CodeHosting in the database.
func syncCodeHosting( //nolint:cyclop // mostly error handling ifs
	gormdb *gorm.DB, publisher models.Publisher, codeHosting []models.CodeHosting,
) ([]models.CodeHosting, error) {
	toRemove := []string{}          // Slice of CodeHosting ids to remove from the database
	toAdd := []models.CodeHosting{} // Slice of CodeHosting to add to the database
	toUpdate := []models.CodeHosting{}

	// Map mirroring the state of CodeHosting for this software in the database,
	// keyed by url
	urlMap := map[string]models.CodeHosting{}

	for _, ch := range publisher.CodeHosting {
		urlMap[ch.URL] = ch
	}

	expected := map[string]bool{}
	for _, ch := range codeHosting {
		expected[ch.URL] = true
	}

	for url, ch := range urlMap {
		if !expected[url] {
			toRemove = append(toRemove, ch.ID)

			delete(urlMap, url)
		}
	}

	for _, wanted := range codeHosting {
		current, exists := urlMap[wanted.URL]
		if !exists {
			// A nil Group leaves the column to its database default, as on POST.
			ch := models.CodeHosting{
				ID: utils.UUIDv4(), URL: wanted.URL, Group: wanted.Group, PublisherID: publisher.ID,
			}

			toAdd = append(toAdd, ch)
			urlMap[wanted.URL] = ch

			continue
		}

		// An entry that omits group keeps the stored flag.
		if wanted.Group != nil && (current.Group == nil || *current.Group != *wanted.Group) {
			current.Group = wanted.Group
			toUpdate = append(toUpdate, current)
			urlMap[wanted.URL] = current
		}
	}

	if len(toRemove) > 0 {
		if err := gormdb.Delete(&models.CodeHosting{}, toRemove).Error; err != nil {
			return nil, err
		}
	}

	for _, ch := range toUpdate {
		if err := gormdb.Model(&models.CodeHosting{ID: ch.ID}).Update("group", *ch.Group).Error; err != nil {
			return nil, err
		}
	}

	if len(toAdd) > 0 {
		if err := gormdb.Create(toAdd).Error; err != nil {
			return nil, err
		}

		for _, ch := range toAdd {
			urlMap[ch.URL] = ch
		}
	}

	retCodeHosting := make([]models.CodeHosting, 0, len(urlMap))
	for _, ch := range urlMap {
		retCodeHosting = append(retCodeHosting, ch)
	}

	return retCodeHosting, nil
}
