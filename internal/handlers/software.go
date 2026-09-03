package handlers

import (
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/handlers/general"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

type SoftwareInterface interface {
	GetAllSoftware(ctx *fiber.Ctx) error
	GetSoftware(ctx *fiber.Ctx) error
	PostSoftware(ctx *fiber.Ctx) error
	PatchSoftware(ctx *fiber.Ctx) error
	DeleteSoftware(ctx *fiber.Ctx) error
	GetSoftwareAnalysis(ctx *fiber.Ctx) error
	PatchSoftwareAnalysis(ctx *fiber.Ctx) error
}

type Software struct {
	db *gorm.DB
}

var (
	errLoadNotFound = errors.New("Software was not found")
	errLoad         = errors.New("error while loading Software")
)

func NewSoftware(db *gorm.DB) *Software {
	return &Software{db: db}
}

// GetAllSoftware gets the list of all software and returns any error encountered.
func (p *Software) GetAllSoftware(ctx *fiber.Ctx) error {
	// Preload will load all the associated aliases, which include
	// also the canonical url. detachCanonicalURL separates them later.
	stmt, err := general.Clauses(ctx, p.db.Preload("Aliases"), "")
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
	software := models.Software{
		ID: utils.UUIDv4(),

		// Manually set the URL and its foreign key because of a limitation in gorm
		URL:           url,
		SoftwareURLID: url.ID,

		Aliases:       aliases,
		PubliccodeYml: softwareReq.PubliccodeYml,
		Active:        softwareReq.Active,
		Vitality:      softwareReq.Vitality,
	}

	if err := p.db.Create(&software).Error; err != nil {
		return writeError(err, errMsg)
	}

	return ctx.JSON(&software)
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

	if err := p.db.Transaction(func(tran *gorm.DB) error {
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

		if err := tran.Updates(&updatedSoftware).Error; err != nil {
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

// DeleteSoftware deletes the software with the given ID.
func (p *Software) DeleteSoftware(ctx *fiber.Ctx) error {
	result := p.db.Select("Aliases", "Bundles").Delete(&models.Software{ID: ctx.Params("id")})

	if result.Error != nil {
		return common.Error(fiber.StatusInternalServerError, "can't delete Software", "db error")
	}

	if result.RowsAffected == 0 {
		return common.Error(fiber.StatusNotFound, "can't delete Software", "Software was not found")
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}

func loadSoftware(gormdb *gorm.DB, software *models.Software, id string) error {
	if err := gormdb.First(&software, "id = ?", id).Error; err != nil {
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
		name:  "Software",
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

	found, err := findOne[models.Software](p.db, ctx.Params("id"), findOptions{
		title: errMsg,
		name:  "Software",
	})
	if err != nil {
		return err
	}

	software := *found

	var incoming common.AnalysisData
	if err := json.Unmarshal(ctx.Body(), &incoming); err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, errMsg, "invalid or malformed JSON")
	}

	// The error here is one of a fixed set of validation messages prefixed
	// with the namespace the caller sent, so it can be returned as is.
	merged, err := injectTouchedAnalysis(software.Analysis, incoming, time.Now())
	if err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, errMsg, err.Error())
	}

	if err := p.db.Model(&software).Update("analysis", merged).Error; err != nil {
		return common.InternalServerError(errMsg)
	}

	return ctx.JSON(merged)
}

// injectTouchedAnalysis validates and injects "t" only into namespaces that
// were added or changed by the patch. Unchanged namespaces keep their original
// "t" value.
func injectTouchedAnalysis(
	original, updated common.AnalysisData,
	now time.Time,
) (common.AnalysisData, error) {
	touched := make(common.AnalysisData)

	for ns, val := range updated {
		origVal, exists := original[ns]
		if !exists || string(origVal) != string(val) {
			touched[ns] = val
		}
	}

	injected, err := common.WithTimestamps(touched, now)
	if err != nil {
		return nil, err //nolint:wrapcheck
	}

	result := make(common.AnalysisData, len(original)+len(updated))
	maps.Copy(result, original)
	maps.Copy(result, updated)
	maps.Copy(result, injected)

	return result, nil
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
