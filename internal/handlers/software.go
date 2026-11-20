package handlers

import (
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
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
	stmt, err := general.Clauses(ctx, p.db, "", "", true)
	if err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, "can't get Software", general.QueryErrorDetail(err))
	}

	stmt = softwareURLFilter(ctx, p.db, stmt)

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

	return patchInTransaction(ctx, p.db, errMsg, func(tran *gorm.DB) (*models.Software, error) {
		software, err := lockSoftware(tran, ctx.Params("id"), errMsg)
		if err != nil {
			return nil, err
		}

		return updateSoftware(ctx, tran, *software)
	})
}

// DeleteSoftware deletes the software with the given ID.
func (p *Software) DeleteSoftware(ctx *fiber.Ctx) error {
	const errMsg = "can't delete Software"

	software := models.Software{ID: ctx.Params("id")}

	if err := models.Transaction(writeDB(ctx, p.db), func(tran *gorm.DB) error {
		// Before the logs, so a log or webhook create waits, see lockParent.
		if err := lockRow[models.Software](tran, "id = ?", software.ID); err != nil {
			return err
		}

		if err := deleteEntityLogs(tran, software); err != nil {
			return err
		}

		return deleteResult(tran.Select("Aliases", "Bundles").Delete(&software))
	}); err != nil {
		return deleteError(err, errMsg, softwareEntityName)
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}

// lockSoftware locks the software with the given id, see lockRow, and
// loads it. Errors are Problem JSON errors, ready to be returned from a
// handler as they are.
func lockSoftware(tran *gorm.DB, id, title string) (*models.Software, error) {
	software := models.Software{}

	err := lockRow[models.Software](tran, "id = ?", id)
	if err == nil {
		err = loadSoftware(tran, &software, id)
	}

	if err != nil {
		if errors.Is(err, errLoadNotFound) {
			return nil, common.Error(fiber.StatusNotFound, title, "Software was not found")
		}

		return nil, common.InternalServerError(title)
	}

	return &software, nil
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

	sort.Slice(software.Aliases, func(a, b int) bool {
		return software.Aliases[a].URL < software.Aliases[b].URL
	})

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

	scope := ""
	if catalogID != nil {
		scope = *catalogID
	}

	url := models.SoftwareURL{ID: utils.UUIDv4(), URL: common.NormalizeURL(softwareReq.URL), CatalogID: scope}

	if err := validateAliasURLs(url.URL, softwareReq.Aliases, errMsg); err != nil {
		return err
	}

	aliases := []models.SoftwareURL{}
	for _, u := range softwareReq.Aliases {
		aliases = append(aliases, models.SoftwareURL{ID: utils.UUIDv4(), URL: common.NormalizeURL(u), CatalogID: scope})
	}

	sort.Slice(aliases, func(a int, b int) bool {
		return aliases[a].URL < aliases[b].URL
	})

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
		if err := lockOwnerCatalog(tran, catalogID, errMsg); err != nil {
			return err
		}

		return tran.Create(software).Error
	}); err != nil {
		return writeError(err, errMsg)
	}

	return ctx.JSON(software)
}

// validateAliasURLs rejects aliases that normalize to the canonical url or
// to an earlier alias. Without it a POST fails on the unique index with a
// 409 that cannot be told apart from a URL owned by another software, and a
// PATCH merges the repeats into one row.
func validateAliasURLs(url string, aliases []string, title string) error {
	seen := make(map[string]struct{}, len(aliases))

	for _, alias := range aliases {
		normalized := common.NormalizeURL(alias)
		if normalized == url {
			return common.Error(fiber.StatusUnprocessableEntity, title,
				fmt.Sprintf("alias %s repeats url", alias))
		}

		if _, exists := seen[normalized]; exists {
			return common.Error(fiber.StatusUnprocessableEntity, title,
				fmt.Sprintf("alias %s repeats another alias", alias))
		}

		seen[normalized] = struct{}{}
	}

	return nil
}

// updateSoftware applies the PATCH body to software and saves it in tran,
// keeping its canonical URL and its aliases in sync. It runs under
// patchInTransaction, with software read after the lock.
func updateSoftware(ctx *fiber.Ctx, tran *gorm.DB, software models.Software) (*models.Software, error) {
	updatedSoftware, err := applyPatch(ctx, &software, patchOptions[models.Software]{
		title:    "can't update Software",
		request:  &common.SoftwarePatch{},
		restore:  restoreSoftware,
		validate: validatePatchedSoftware,
	})
	if err != nil {
		return nil, err
	}

	updatedSoftware.URL.URL = common.NormalizeURL(updatedSoftware.URL.URL)

	// Slice of aliases that we expect to be in the database after the PATCH
	expectedAliases := make([]string, 0, len(updatedSoftware.Aliases))
	for _, alias := range updatedSoftware.Aliases {
		expectedAliases = append(expectedAliases, common.NormalizeURL(alias.URL))
	}

	//nolint:gocritic // it's fine, we want to append to another slice
	currentURLs := append(software.Aliases, software.URL)

	updatedURL, aliases, err := syncAliases(
		tran,
		software.ID,
		software.URL.CatalogID,
		currentURLs,
		updatedSoftware.URL.URL,
		expectedAliases,
	)
	if err != nil {
		return nil, err
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
		return nil, err
	}

	updatedSoftware.Aliases = aliases

	// Sort the aliases to always have a consistent output
	sort.Slice(updatedSoftware.Aliases, func(a int, b int) bool {
		return updatedSoftware.Aliases[a].URL < updatedSoftware.Aliases[b].URL
	})

	return &updatedSoftware, nil
}

// syncAliases synchs the SoftwareURLs for a `Software` in the database to reflect the
// passed list of `expectedAliases` and the canonical `url`.
//
// It returns the new canonical SoftwareURL and the new slice of aliases or an error if any.
func syncAliases( //nolint:cyclop // mostly error handling ifs
	gormdb *gorm.DB,
	softwareID string,
	catalogID string,
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
			su := models.SoftwareURL{ID: utils.UUIDv4(), URL: urlStr, SoftwareID: softwareID, CatalogID: catalogID}

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

	return ctx.JSON(analysisOrEmpty(software.Analysis))
}

// PatchSoftwareAnalysis merges the incoming analysis namespaces into the stored analysis.
// The request body is an AnalysisData object (namespace → arbitrary JSON with "v" field).
func (p *Software) PatchSoftwareAnalysis(ctx *fiber.Ctx) error {
	const errMsg = "can't update Software analysis"

	software := models.Software{ID: ctx.Params("id")}

	return patchAnalysis(ctx, p.db, &software, analysisOptions{
		title: errMsg,
		name:  softwareEntityName,
		current: func() (common.AnalysisData, error) {
			found, err := findOne[models.Software](p.db, software.ID, findOptions{
				title: errMsg,
				name:  softwareEntityName,
			})
			if err != nil {
				return nil, err
			}

			return found.Analysis, nil
		},
	})
}

// softwareURLFilter matches every owner of a canonical URL or alias. The
// caller's catalog scope still applies, while the global list can return
// the same repository from several catalogs.
func softwareURLFilter(ctx *fiber.Ctx, gormdb, stmt *gorm.DB) *gorm.DB {
	url := common.NormalizeURL(ctx.Query("url", ""))
	if url == "" {
		return stmt
	}

	owners := gormdb.Model(&models.SoftwareURL{}).Select("software_id").Where("url = ?", url)

	return stmt.Where("id IN (?)", owners)
}

// listSoftware writes one page of the software matched by stmt and loads
// its aliases. stmt must not Preload("URL"): that makes gorm guess a
// has one through the software_id column and load an arbitrary url. The
// ?from and ?to window is the caller's, GET /software applies it and
// the catalog software list does not declare it.
func listSoftware(ctx *fiber.Ctx, stmt *gorm.DB) error {
	stmt = stmt.Preload("Aliases")

	software, cursor, err := paginate[models.Software](ctx, stmt, listOptions{
		title:      "can't get Software",
		activeOnly: true,
	})
	if err != nil {
		return err
	}

	for i := range software {
		aliases := software[i].Aliases
		sort.Slice(aliases, func(a, b int) bool {
			return aliases[a].URL < aliases[b].URL
		})
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

				swr.Aliases = slices.Delete(swr.Aliases, aliasIdx, aliasIdx+1)

				break
			}
		}
	}
}
