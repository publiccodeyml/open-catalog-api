package handlers

import (
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/handlers/general"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

// rootCatalogID is the path parameter value that refers to the implicit root
// catalog (resources with catalog_id IS NULL).
const rootCatalogID = "∅"

type Catalog struct {
	db *gorm.DB
}

func NewCatalog(db *gorm.DB) *Catalog {
	return &Catalog{db: db}
}

// isRoot reports whether the given catalog represents the implicit root.
// A nil catalog (no row found at the ∅ sentinel) and a row whose
// alternativeId is ∅ are both treated as root: their resources have
// catalog_id IS NULL.
func isRoot(catalog *models.Catalog) bool {
	if catalog == nil {
		return true
	}

	return catalog.AlternativeID != nil && *catalog.AlternativeID == rootCatalogID
}

// catalogScope returns a GORM scope that filters by catalog.
// Root catalog (implicit or materialized as the ∅ alias) means catalog_id IS NULL.
func catalogScope(catalog *models.Catalog) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if isRoot(catalog) {
			return db.Where("catalog_id IS NULL")
		}

		return db.Where("catalog_id = ?", catalog.ID)
	}
}

// catalogOwnerID is the catalog_id a resource created under catalog carries.
// Resources of the root catalog have none.
func catalogOwnerID(catalog *models.Catalog) *string {
	if isRoot(catalog) {
		return nil
	}

	return &catalog.ID
}

// belongsToCatalog reports whether a resource whose catalog_id is catalogID
// is a resource of catalog. On the root the column is NULL.
func belongsToCatalog(catalog *models.Catalog, catalogID *string) bool {
	if isRoot(catalog) {
		return catalogID == nil
	}

	return catalogID != nil && *catalogID == catalog.ID
}

// GetCatalogs gets the list of all catalogs.
func (c *Catalog) GetCatalogs(ctx *fiber.Ctx) error {
	return list[models.Catalog](ctx, c.db.Preload("Sources"), listOptions{
		title:      "can't get Catalogs",
		activeOnly: true,
	})
}

// GetCatalog gets the catalog with the given id.
func (c *Catalog) GetCatalog(ctx *fiber.Ctx) error {
	id, _ := url.PathUnescape(ctx.Params("id"))

	catalog, err := resolveCatalog(c.db, id, "Sources")
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, "can't get Catalog", "Catalog was not found")
		}

		return common.Error(fiber.StatusInternalServerError, "can't get Catalog", fiber.ErrInternalServerError.Message)
	}

	if catalog == nil {
		// Root catalog: return a synthetic representation.
		return common.Error(fiber.StatusNotFound, "can't get Catalog", "Catalog was not found")
	}

	return ctx.JSON(catalog)
}

// PostCatalog creates a new catalog.
// When alternativeId is "∅" the new row materializes the root catalog: it
// holds the configuration (sources are not allowed; resources are still
// addressed via catalog_id IS NULL). For any other catalog at least one
// source is required.
func (c *Catalog) PostCatalog(ctx *fiber.Ctx) error {
	const errMsg = "can't create Catalog"

	request := new(common.CatalogPost)

	if err := common.ValidateRequestEntity(ctx, request, errMsg); err != nil {
		return err //nolint:wrapcheck
	}

	asRoot := request.AlternativeID != nil && *request.AlternativeID == rootCatalogID

	if asRoot && len(request.Sources) > 0 {
		return common.Error(fiber.StatusUnprocessableEntity, errMsg,
			"sources are not allowed on the root catalog")
	}

	if !asRoot && len(request.Sources) == 0 {
		return common.Error(fiber.StatusUnprocessableEntity, errMsg, "sources is required")
	}

	sources := buildSources(request.Sources)

	catalog := &models.Catalog{
		ID:                  utils.UUIDv4(),
		Name:                request.Name,
		AlternativeID:       request.AlternativeID,
		Active:              request.Active,
		Scopes:              request.Scopes,
		PublishersNamespace: request.PublishersNamespace,
		Sources:             sources,
	}

	if err := c.db.Create(catalog).Error; err != nil {
		return writeError(err, errMsg)
	}

	return ctx.JSON(catalog)
}

// PatchCatalog updates the catalog with the given id.
func (c *Catalog) PatchCatalog(ctx *fiber.Ctx) error { //nolint:cyclop
	const errMsg = "can't update Catalog"

	catalogID, _ := url.PathUnescape(ctx.Params("id"))

	resolved, err := resolveCatalog(c.db, catalogID, "Sources")
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
		}

		return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
	}

	if resolved == nil {
		return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
	}

	catalog := *resolved

	updatedCatalog, err := applyPatch(ctx, &catalog, patchOptions[models.Catalog]{
		title:    errMsg,
		request:  new(common.CatalogPatch),
		restore:  restoreCatalog,
		validate: validatePatchedCatalog,
	})
	if err != nil {
		return err
	}

	if isRoot(&catalog) {
		if updatedCatalog.AlternativeID == nil || *updatedCatalog.AlternativeID != rootCatalogID {
			return common.Error(fiber.StatusUnprocessableEntity, errMsg,
				"alternativeId on the root catalog cannot be changed")
		}
	}

	sourcesInput := make([]common.SourceInput, 0, len(updatedCatalog.Sources))
	for _, src := range updatedCatalog.Sources {
		sourcesInput = append(sourcesInput, common.SourceInput{
			URL:    src.URL,
			Driver: src.Driver,
			Args:   src.Args,
		})
	}

	if isRoot(&catalog) {
		if len(sourcesInput) > 0 {
			return common.Error(fiber.StatusUnprocessableEntity, errMsg,
				"sources are not allowed on the root catalog")
		}
	} else if len(sourcesInput) == 0 {
		return common.Error(fiber.StatusUnprocessableEntity, errMsg, "sources must not be empty")
	}

	if err := c.db.Transaction(func(tran *gorm.DB) error {
		sources, err := syncSources(tran, catalog, sourcesInput)
		if err != nil {
			return err
		}

		updatedCatalog.Sources = nil

		err = updateColumns(tran, &updatedCatalog,
			"Name", "AlternativeID", "Active", "PublishersNamespace", "Scopes")
		if err != nil {
			return err
		}

		updatedCatalog.Sources = sources

		return nil
	}); err != nil {
		return writeError(err, errMsg)
	}

	return ctx.JSON(&updatedCatalog)
}

// DeleteCatalog deletes the catalog with the given id.
// Returns 409 if the catalog still has associated publishers or software.
// On the root (∅) the count of attached resources is taken from rows with
// catalog_id IS NULL, since root resources are never tied to the row's UUID.
func (c *Catalog) DeleteCatalog(ctx *fiber.Ctx) error { //nolint:cyclop
	const errMsg = "can't delete Catalog"

	catalogID, _ := url.PathUnescape(ctx.Params("id"))

	resolved, err := resolveCatalog(c.db, catalogID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
		}

		return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
	}

	if resolved == nil {
		return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
	}

	catalog := *resolved

	var conflictErr error

	if err := c.db.Transaction(func(tran *gorm.DB) error {
		var publisherCount, softwareCount int64

		pubScope := tran.Model(&models.Publisher{}).Scopes(catalogScope(&catalog))
		if err := pubScope.Count(&publisherCount).Error; err != nil {
			return err
		}

		swScope := tran.Model(&models.Software{}).Scopes(catalogScope(&catalog))
		if err := swScope.Count(&softwareCount).Error; err != nil {
			return err
		}

		if publisherCount > 0 || softwareCount > 0 {
			conflictErr = common.Error(fiber.StatusConflict, errMsg, "Catalog still has associated publishers or software")

			return nil
		}

		if err := tran.Where("catalog_id = ?", catalog.ID).Delete(&models.CatalogSource{}).Error; err != nil {
			return err
		}

		return tran.Where("id = ?", catalog.ID).Delete(&models.Catalog{}).Error
	}); err != nil {
		return common.Error(fiber.StatusInternalServerError, errMsg, "db error")
	}

	if conflictErr != nil {
		return conflictErr //nolint:wrapcheck
	}

	return ctx.SendStatus(fiber.StatusNoContent)
}

// GetCatalogPublishers lists publishers belonging to the given catalog.
func (c *Catalog) GetCatalogPublishers(ctx *fiber.Ctx) error {
	id := ctx.Params("id")

	catalog, err := resolveCatalog(c.db, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, "can't get Publishers", "Catalog was not found")
		}

		return common.Error(fiber.StatusInternalServerError, "can't get Publishers", fiber.ErrInternalServerError.Message)
	}

	stmt := c.db.Preload(codeHostingAssociation).Scopes(catalogScope(catalog))

	return list[models.Publisher](ctx, stmt, listOptions{
		title:      "can't get Publishers",
		activeOnly: true,
	})
}

// PostCatalogPublisher creates a publisher belonging to the given catalog.
// The catalog is resolved from the URL; any catalogId in the body is ignored.
func (c *Catalog) PostCatalogPublisher(ctx *fiber.Ctx) error {
	const errMsg = "can't create Publisher"

	catalog, err := resolveCatalog(c.db, ctx.Params("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
		}

		return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
	}

	return createPublisher(ctx, c.db, catalogOwnerID(catalog))
}

// PatchCatalogPublisher updates a publisher that belongs to the given catalog.
func (c *Catalog) PatchCatalogPublisher(ctx *fiber.Ctx) error {
	const errMsg = "can't update Publisher"

	catalog, err := resolveCatalog(c.db, ctx.Params("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
		}

		return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
	}

	found, err := findOne[models.Publisher](c.db, ctx.Params("publisherId"), findOptions{
		title:           errMsg,
		name:            publisherEntityName,
		byAlternativeID: true,
		preloads:        []string{codeHostingAssociation},
	})
	if err != nil {
		return err
	}

	if !belongsToCatalog(catalog, found.CatalogID) {
		return common.Error(fiber.StatusNotFound, errMsg, "Publisher was not found")
	}

	return updatePublisher(ctx, c.db, *found)
}

// PostCatalogSoftware creates software belonging to the given catalog.
// The catalog is resolved from the URL; any catalogId in the body is ignored.
func (c *Catalog) PostCatalogSoftware(ctx *fiber.Ctx) error {
	const errMsg = "can't create Software"

	catalog, err := resolveCatalog(c.db, ctx.Params("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
		}

		return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
	}

	return createSoftware(ctx, c.db, catalogOwnerID(catalog))
}

// PatchCatalogSoftware updates software that belongs to the given catalog.
func (c *Catalog) PatchCatalogSoftware(ctx *fiber.Ctx) error {
	const errMsg = "can't update Software"

	catalog, err := resolveCatalog(c.db, ctx.Params("id"))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
		}

		return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
	}

	software := models.Software{}

	if err := loadSoftware(c.db, &software, ctx.Params("softwareId")); err != nil {
		if errors.Is(err, errLoadNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Software was not found")
		}

		return common.Error(fiber.StatusInternalServerError, errMsg, fiber.ErrInternalServerError.Message)
	}

	if !belongsToCatalog(catalog, software.CatalogID) {
		return common.Error(fiber.StatusNotFound, errMsg, "Software was not found")
	}

	return updateSoftware(ctx, c.db, software)
}

// GetCatalogSoftware lists software belonging to the given catalog.
func (c *Catalog) GetCatalogSoftware(ctx *fiber.Ctx) error {
	id := ctx.Params("id")

	catalog, err := resolveCatalog(c.db, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, "can't get Software", "Catalog was not found")
		}

		return common.Error(fiber.StatusInternalServerError, "can't get Software", fiber.ErrInternalServerError.Message)
	}

	stmt, err := general.Clauses(ctx, c.db.Preload("Aliases").Scopes(catalogScope(catalog)), "")
	if err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, "can't get Software", general.QueryErrorDetail(err))
	}

	stmt, found, err := softwareURLFilter(ctx, c.db, stmt, "can't get Software")
	if err != nil {
		return err
	}

	if !found {
		return ctx.JSON(fiber.Map{"data": []any{}, "links": general.PaginationLinks{}})
	}

	return listSoftware(ctx, stmt)
}

// buildSources converts SourceInput slice to CatalogSource models.
func buildSources(inputs []common.SourceInput) []models.CatalogSource {
	sources := make([]models.CatalogSource, 0, len(inputs))

	for _, inp := range inputs {
		sources = append(sources, models.CatalogSource{
			ID:     utils.UUIDv4(),
			Driver: inp.Driver,
			URL:    common.NormalizeURL(inp.URL),
			Args:   inp.Args,
		})
	}

	return sources
}

// sameString reports whether two optional strings are both absent or hold
// the same value. A source whose driver the patch omits keeps no driver.
func sameString(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}

	return *a == *b
}

// syncSources brings the catalog_sources table in line with the desired state.
// Sources are matched by URL; removed if absent, added if new.
func syncSources( //nolint:cyclop,funlen
	gormdb *gorm.DB,
	catalog models.Catalog,
	desired []common.SourceInput,
) ([]models.CatalogSource, error) {
	toRemove := []string{}
	toAdd := []models.CatalogSource{}
	toUpdate := []models.CatalogSource{}

	urlMap := map[string]models.CatalogSource{}
	for _, src := range catalog.Sources {
		urlMap[src.URL] = src
	}

	desiredSet := map[string]common.SourceInput{}
	for _, inp := range desired {
		desiredSet[common.NormalizeURL(inp.URL)] = inp
	}

	for srcURL, src := range urlMap {
		if _, ok := desiredSet[srcURL]; !ok {
			toRemove = append(toRemove, src.ID)

			delete(urlMap, srcURL)
		}
	}

	for srcURL, inp := range desiredSet {
		if existing, ok := urlMap[srcURL]; ok {
			changed := false

			if !sameString(existing.Driver, inp.Driver) {
				existing.Driver = inp.Driver
				changed = true
			}

			if !slices.Equal(existing.Args, inp.Args) {
				existing.Args = inp.Args
				changed = true
			}

			if changed {
				toUpdate = append(toUpdate, existing)
				urlMap[srcURL] = existing
			}
		} else {
			src := models.CatalogSource{
				ID:        utils.UUIDv4(),
				Driver:    inp.Driver,
				URL:       common.NormalizeURL(srcURL),
				Args:      inp.Args,
				CatalogID: catalog.ID,
			}
			toAdd = append(toAdd, src)
			urlMap[srcURL] = src
		}
	}

	if len(toRemove) > 0 {
		if err := gormdb.Delete(&models.CatalogSource{}, toRemove).Error; err != nil {
			return nil, err
		}
	}

	if len(toAdd) > 0 {
		if err := gormdb.Create(toAdd).Error; err != nil {
			return nil, err
		}
	}

	for _, src := range toUpdate {
		if err := gormdb.Save(&src).Error; err != nil {
			return nil, err
		}
	}

	ret := make([]models.CatalogSource, 0, len(urlMap))
	for _, src := range urlMap {
		ret = append(ret, src)
	}

	return ret, nil
}

// resolveCatalog looks up a catalog by id or alternativeId.
// If id is rootCatalogID and no catalog with that id exists, it returns nil
// (meaning: filter by catalog_id IS NULL).
// Optional preloads (e.g. "Sources") are applied to the query.
func resolveCatalog(gormdb *gorm.DB, rawID string, preloads ...string) (*models.Catalog, error) {
	catalogID, err := url.PathUnescape(rawID)
	if err != nil {
		catalogID = rawID
	}

	var catalog models.Catalog

	stmt := gormdb
	for _, p := range preloads {
		stmt = stmt.Preload(p)
	}

	dbErr := stmt.First(&catalog, "id = ? OR alternative_id = ?", catalogID, catalogID).Error
	if dbErr == nil {
		return &catalog, nil
	}

	if errors.Is(dbErr, gorm.ErrRecordNotFound) && catalogID == rootCatalogID {
		return nil, nil //nolint:nilnil
	}

	return nil, dbErr
}

// GetCatalogAnalysis returns the analysis data for the catalog with the given id.
func (c *Catalog) GetCatalogAnalysis(ctx *fiber.Ctx) error {
	const errMsg = "can't get Catalog analysis"

	id, _ := url.PathUnescape(ctx.Params("id"))

	catalog, err := resolveCatalog(c.db, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
		}

		return common.InternalServerError(errMsg)
	}

	if catalog == nil {
		return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
	}

	if catalog.Analysis == nil {
		return ctx.JSON(common.AnalysisData{})
	}

	return ctx.JSON(catalog.Analysis)
}

// PatchCatalogAnalysis merges the incoming analysis namespaces into the stored analysis.
// The request body is an AnalysisData object (namespace → arbitrary JSON with "v" field).
func (c *Catalog) PatchCatalogAnalysis(ctx *fiber.Ctx) error {
	const errMsg = "can't update Catalog analysis"

	id, _ := url.PathUnescape(ctx.Params("id"))

	catalog, err := resolveCatalog(c.db, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
		}

		return common.InternalServerError(errMsg)
	}

	if catalog == nil {
		return common.Error(fiber.StatusNotFound, errMsg, "Catalog was not found")
	}

	var incoming common.AnalysisData
	if err := json.Unmarshal(ctx.Body(), &incoming); err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, errMsg, "invalid or malformed JSON")
	}

	// The error here is one of a fixed set of validation messages prefixed
	// with the namespace the caller sent, so it can be returned as is.
	merged, err := injectTouchedAnalysis(catalog.Analysis, incoming, time.Now())
	if err != nil {
		return common.Error(fiber.StatusUnprocessableEntity, errMsg, err.Error())
	}

	if err := c.db.Model(catalog).Update("analysis", merged).Error; err != nil {
		return common.InternalServerError(errMsg)
	}

	return ctx.JSON(merged)
}
