package handlers

import (
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/database"
	"github.com/publiccodeyml/open-catalog-api/internal/handlers/general"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

type Software struct {
	db *gorm.DB
}

const softwareEntityName = "Software"

var (
	errLoadNotFound = errors.New("Software was not found")
	errLoad         = errors.New("error while loading Software")
)

func NewSoftware(db *gorm.DB) *Software {
	return &Software{db: db}
}

// GetAllSoftware gets the list of all software and returns any error encountered.
func (p *Software) GetAllSoftware(ctx *fiber.Ctx) error {
	stmt, err := general.Clauses(ctx, p.db.Preload("Aliases"), "", "")
	if err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, "can't get Software", general.QueryErrorDetail(err))
	}

	stmt, found, err := softwareURLFilter(ctx, p.db, stmt, "can't get Software")
	if err != nil {
		return err
	}

	if !found {
		return ctx.JSON(fiber.Map{"data": []any{}, "links": general.PaginationLinks{}})
	}

	return listSoftware(ctx, stmt)
}

// GetSoftware gets the software with the given ID and returns any error encountered.
func (p *Software) GetSoftware(ctx *fiber.Ctx) error {
	const errMsg = "can't get Software"

	software := models.Software{}

	if err := loadSoftware(p.db, &software, ctx.Params("id")); err != nil {
		if errors.Is(err, errLoadNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Software was not found")
		}

		return common.InternalServerError(errMsg)
	}

	return ctx.JSON(&software)
}

// PostSoftware creates a new software.
func (p *Software) PostSoftware(ctx *fiber.Ctx) error {
	return createSoftware(ctx, writeDB(ctx, p.db), nil)
}

// PatchSoftware updates the software with the given ID.
func (p *Software) PatchSoftware(ctx *fiber.Ctx) error {
	const errMsg = "can't update Software"

	software := models.Software{}

	if err := loadSoftware(p.db, &software, ctx.Params("id")); err != nil {
		if errors.Is(err, errLoadNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Software was not found")
		}

		return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
	}

	return updateSoftware(ctx, writeDB(ctx, p.db), software)
}

// DeleteSoftware deletes the software with the given ID. The row is
// looked up first: gorm runs the AfterDelete hook even when the delete
// matched nothing, and the hook would record an event for a software
// that never existed.
func (p *Software) DeleteSoftware(ctx *fiber.Ctx) error {
	const errMsg = "can't delete Software"

	found, err := findOne[models.Software](p.db, ctx.Params("id"), findOptions{title: errMsg, name: softwareEntityName})
	if err != nil {
		return err
	}

	software := *found

	if err := models.Transaction(writeDB(ctx, p.db), func(tran *gorm.DB) error {
		if err := deleteEntityLogs(tran, software); err != nil {
			return err
		}

		return tran.Select("Aliases", "Bundles").Delete(&software).Error
	}); err != nil {
		return common.InternalServerError(errMsg)
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}

func loadSoftware(gormdb *gorm.DB, software *models.Software, id string) error {
	if err := gormdb.First(software, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errLoadNotFound
		}

		return errLoad
	}

	if err := gormdb.
		Where("software_id = ? AND id <> ?", software.ID, software.SoftwareURLID).Find(&software.Aliases).
		Error; err != nil {
		return errLoad
	}

	if err := gormdb.Where("id = ?", software.SoftwareURLID).First(&software.URL).Error; err != nil {
		return errLoad
	}

	return nil
}

// createSoftware creates a software from the request body. catalogID owns
// it, nil for the root catalog and for the catalog agnostic endpoint.
func createSoftware(ctx *fiber.Ctx, gormdb *gorm.DB, catalogID *string) error {
	const errMsg = "can't create Software"

	softwareReq := new(common.SoftwarePost)

	if err := common.ValidateRequestEntity(ctx, softwareReq, errMsg); err != nil {
		return err //nolint:wrapcheck
	}

	aliases := []models.SoftwareURL{}
	for _, u := range softwareReq.Aliases {
		aliases = append(aliases, models.SoftwareURL{ID: utils.UUIDv4(), URL: common.NormalizeURL(u)})
	}

	url := models.SoftwareURL{ID: utils.UUIDv4(), URL: common.NormalizeURL(softwareReq.URL)}
	software := &models.Software{
		ID: utils.UUIDv4(),

		// Manually set the URL and its foreign key because of a limitation in gorm
		URL:           url,
		SoftwareURLID: url.ID,

		CatalogID:     catalogID,
		Aliases:       aliases,
		PubliccodeYml: softwareReq.PubliccodeYml,
		Active:        softwareReq.Active,
		Vitality:      softwareReq.Vitality,
	}

	if err := models.Transaction(gormdb, func(tran *gorm.DB) error {
		return tran.Create(software).Error
	}); err != nil {
		return writeError(err, errMsg)
	}

	return ctx.JSON(software)
}

// updateSoftware applies the PATCH body to software and saves it, keeping
// its canonical URL and its aliases in sync.
func updateSoftware(ctx *fiber.Ctx, gormdb *gorm.DB, software models.Software) error {
	const errMsg = "can't update Software"

	updatedSoftware, err := applyPatch(ctx, &software, patchOptions[models.Software]{
		title:    errMsg,
		request:  &common.SoftwarePatch{},
		restore:  restoreSoftware,
		validate: validatePatchedSoftware,
	})
	if err != nil {
		return err
	}

	updatedSoftware.URL.URL = common.NormalizeURL(updatedSoftware.URL.URL)

	// Slice of aliases that we expect to be in the database after the PATCH
	expectedAliases := make([]string, 0, len(updatedSoftware.Aliases))
	for _, alias := range updatedSoftware.Aliases {
		expectedAliases = append(expectedAliases, common.NormalizeURL(alias.URL))
	}

	if err := models.Transaction(gormdb, func(tran *gorm.DB) error {
		//nolint:gocritic // it's fine, we want to append to another slice
		currentURLs := append(software.Aliases, software.URL)

		updatedURL, aliases, err := syncAliases(
			tran,
			software.ID,
			currentURLs,
			updatedSoftware.URL.URL,
			expectedAliases,
		)
		if err != nil {
			return err
		}

		// Manually set the canonical URL via the foreign key because of a limitation in gorm
		updatedSoftware.SoftwareURLID = updatedURL.ID
		updatedSoftware.URL = *updatedURL

		// Set Aliases to a zero value, so it's not touched by gorm's Update(),
		// because we handle the alias manually
		updatedSoftware.Aliases = []models.SoftwareURL{}

		err = updateColumns(tran, &updatedSoftware,
			"PubliccodeYml", "Active", "Vitality", "SoftwareURLID")
		if err != nil {
			return err
		}

		updatedSoftware.Aliases = aliases

		return nil
	}); err != nil {
		return writeError(err, errMsg)
	}

	// Sort the aliases to always have a consistent output
	sort.Slice(updatedSoftware.Aliases, func(a int, b int) bool {
		return updatedSoftware.Aliases[a].URL < updatedSoftware.Aliases[b].URL
	})

	return ctx.JSON(&updatedSoftware)
}

// syncAliases synchs the SoftwareURLs for a `Software` in the database to reflect the
// passed list of `expectedAliases` and the canonical `url`.
//
// It returns the new canonical SoftwareURL and the new slice of aliases or an error if any.
func syncAliases( //nolint:cyclop // mostly error handling ifs
	gormdb *gorm.DB,
	softwareID string,
	currentURLs []models.SoftwareURL,
	expectedURL string,
	expectedAliases []string,
) (*models.SoftwareURL, []models.SoftwareURL, error) {
	toRemove := []string{}          // Slice of SoftwareURL ids to remove from the database
	toAdd := []models.SoftwareURL{} // Slice of SoftwareURLs to add to the database

	// Map mirroring the state of SoftwareURLs for this software in the database,
	// keyed by url
	urlMap := map[string]models.SoftwareURL{}

	for _, url := range currentURLs {
		urlMap[url.URL] = url
	}

	//nolint:gocritic // it's fine, we want to another slice
	allSoftwareURLs := append(expectedAliases, expectedURL)

	for urlStr, softwareURL := range urlMap {
		if !slices.Contains(allSoftwareURLs, urlStr) {
			toRemove = append(toRemove, softwareURL.ID)

			delete(urlMap, urlStr)
		}
	}

	for _, urlStr := range allSoftwareURLs {
		_, exists := urlMap[urlStr]
		if !exists {
			su := models.SoftwareURL{ID: utils.UUIDv4(), URL: urlStr, SoftwareID: softwareID}

			toAdd = append(toAdd, su)
			urlMap[urlStr] = su
		}
	}

	if len(toRemove) > 0 {
		if err := gormdb.Delete(&models.SoftwareURL{}, toRemove).Error; err != nil {
			return nil, nil, err
		}
	}

	if len(toAdd) > 0 {
		if err := gormdb.Create(toAdd).Error; err != nil {
			return nil, nil, err
		}
	}

	updatedURL := urlMap[expectedURL]

	// Remove the canonical URL from the rest of the URLs, so we can return
	// URL and aliases in different fields.
	delete(urlMap, expectedURL)

	aliases := make([]models.SoftwareURL, 0, len(urlMap))
	for _, alias := range urlMap {
		aliases = append(aliases, alias)
	}

	return &updatedURL, aliases, nil
}

// GetSoftwareAnalysis returns the analysis data for the software with the given ID.
func (p *Software) GetSoftwareAnalysis(ctx *fiber.Ctx) error {
	const errMsg = "can't get Software analysis"

	software, err := findOne[models.Software](p.db, ctx.Params("id"), findOptions{
		title: errMsg,
		name:  softwareEntityName,
	})
	if err != nil {
		return err
	}

	if software.Analysis == nil {
		return ctx.JSON(common.AnalysisData{})
	}

	return ctx.JSON(software.Analysis)
}

// PatchSoftwareAnalysis merges the incoming analysis namespaces into the stored analysis.
// The request body is an AnalysisData object (namespace → arbitrary JSON with "v" field).
func (p *Software) PatchSoftwareAnalysis(ctx *fiber.Ctx) error {
	const errMsg = "can't update Software analysis"

	var incoming common.AnalysisData
	if err := json.Unmarshal(ctx.Body(), &incoming); err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, errMsg, "invalid or malformed JSON")
	}

	patch, err := common.WithTimestamps(incoming, time.Now())
	if err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, errMsg, err.Error())
	}

	if len(patch) == 0 {
		software, err := findOne[models.Software](p.db, ctx.Params("id"), findOptions{
			title: errMsg,
			name:  softwareEntityName,
		})
		if err != nil {
			return err
		}

		return ctx.JSON(analysisOrEmpty(software.Analysis))
	}

	software := models.Software{ID: ctx.Params("id")}

	merged, err := database.MergeAnalysis(writeDB(ctx, p.db), &software, patch)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Software was not found")
		}

		return common.InternalServerError(errMsg)
	}

	return ctx.JSON(merged)
}

// softwareURLFilter narrows stmt to the software owning the ?url query
// parameter. found is false when no software has that url, so the caller
// answers with an empty list instead of running the query. title is the
// Problem JSON title of the 500 the caller would have written itself.
func softwareURLFilter(ctx *fiber.Ctx, gormdb, stmt *gorm.DB, title string) (*gorm.DB, bool, error) {
	url := common.NormalizeURL(ctx.Query("url", ""))
	if url == "" {
		return stmt, true, nil
	}

	var softwareURL models.SoftwareURL

	if err := gormdb.First(&softwareURL, "url = ?", url).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return stmt, false, nil
		}

		return nil, false, common.InternalServerError(title)
	}

	return stmt.Where("id = ?", softwareURL.SoftwareID), true, nil
}

// listSoftware writes one page of the software matched by stmt. stmt must
// Preload("Aliases") and nothing else: Preload("URL") makes gorm guess a
// has one through the software_id column and load an arbitrary url.
func listSoftware(ctx *fiber.Ctx, stmt *gorm.DB) error {
	software, cursor, err := paginate[models.Software](ctx, stmt, listOptions{
		title:       "can't get Software",
		activeOnly:  true,
		skipClauses: true,
	})
	if err != nil {
		return err
	}

	detachCanonicalURL(software)

	return listJSON(ctx, software, cursor)
}

// detachCanonicalURL moves the canonical url out of Aliases into URL.
// Preload("Aliases") loads it together with the other aliases because
// of a limitation in gorm, and the API exposes it as its own field.
func detachCanonicalURL(software []models.Software) {
	for swIdx := range software {
		swr := &software[swIdx]

		for aliasIdx := range swr.Aliases {
			alias := &swr.Aliases[aliasIdx]

			if alias.ID == swr.SoftwareURLID {
				swr.URL = *alias

				swr.Aliases[aliasIdx] = swr.Aliases[len(swr.Aliases)-1]
				swr.Aliases = swr.Aliases[:len(swr.Aliases)-1]

				break
			}
		}
	}
}
