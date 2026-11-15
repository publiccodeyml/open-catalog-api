package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
)

// validatePatchedSoftware applies the same field rules used by a merge patch
// to the complete entity produced by an RFC 6902 patch. JSON Patch operates on
// the response model directly, so it otherwise bypasses the request DTO.
func validatePatchedSoftware(software models.Software, title string) error {
	if err := refuseNullActive(software.Active, title); err != nil {
		return err
	}

	aliases := make([]string, 0, len(software.Aliases))
	for _, alias := range software.Aliases {
		aliases = append(aliases, alias.URL)
	}

	request := common.SoftwarePatch{
		URL:           &software.URL.URL,
		Aliases:       &aliases,
		PubliccodeYml: &software.PubliccodeYml,
		Active:        software.Active,
		Vitality:      software.Vitality,
	}

	return validatePatchedEntity(request, title)
}

func validatePatchedPublisher(publisher models.Publisher, title string) error {
	if err := refuseNullActive(publisher.Active, title); err != nil {
		return err
	}

	codeHosting := make([]common.CodeHosting, 0, len(publisher.CodeHosting))
	for _, codeHost := range publisher.CodeHosting {
		codeHosting = append(codeHosting, common.CodeHosting{
			URL:   codeHost.URL,
			Group: codeHost.Group,
		})
	}

	request := common.PublisherPatch{
		CodeHosting:   &codeHosting,
		Description:   &publisher.Description,
		Email:         publisher.Email,
		Active:        publisher.Active,
		AlternativeID: publisher.AlternativeID,
	}

	return validatePatchedEntity(request, title)
}

func validatePatchedCatalog(catalog models.Catalog, title string) error {
	if err := refuseNullActive(catalog.Active, title); err != nil {
		return err
	}

	sources := make([]common.SourceInput, 0, len(catalog.Sources))
	for _, source := range catalog.Sources {
		sources = append(sources, common.SourceInput{
			Driver: source.Driver,
			URL:    source.URL,
			Args:   source.Args,
		})
	}

	request := common.CatalogPatch{
		Name:                &catalog.Name,
		AlternativeID:       catalog.AlternativeID,
		Active:              catalog.Active,
		PublishersNamespace: catalog.PublishersNamespace,
		Scopes:              &catalog.Scopes,
	}

	// An empty sources list is valid for the root catalog. The catalog handler
	// performs the context-dependent empty/non-empty check immediately after
	// this schema validation.
	if len(sources) > 0 {
		request.Sources = &sources
	}

	return validatePatchedEntity(request, title)
}

func validatePatchedLog(log models.Log, title string) error {
	return validatePatchedEntity(common.Log{Message: log.Message}, title)
}

func validatePatchedBundle(bundle, current models.Bundle, title string) error {
	if err := refuseNullActive(bundle.Active, title); err != nil {
		return err
	}

	// A null or removed list reaches here as a nil slice, which the
	// omitempty rule skips. The bundle would then lose all its software
	// and answer null where the schema requires an array.
	if bundle.SoftwareIDs == nil {
		return common.Error(fiber.StatusUnprocessableEntity, title, "softwareIds cannot be null")
	}

	request := common.BundlePatch{
		Name:        &bundle.Name,
		Description: bundle.Description,
		Active:      bundle.Active,
		SoftwareIDs: &bundle.SoftwareIDs,
	}

	// Deleting a software removes it from its bundles, so a bundle can
	// be stored with no software at all. A patch that leaves the empty
	// list alone must still be able to rename or disable the bundle.
	if len(bundle.SoftwareIDs) == 0 && len(current.SoftwareIDs) == 0 {
		request.SoftwareIDs = nil
	}

	return validatePatchedEntity(request, title)
}

func restoreCatalog(updated, current *models.Catalog) {
	updated.ID = current.ID
	updated.CreatedAt = current.CreatedAt
}

func restorePublisher(updated, current *models.Publisher) {
	updated.ID = current.ID
	updated.CatalogID = current.CatalogID
	updated.CreatedAt = current.CreatedAt
}

func restoreSoftware(updated, current *models.Software) {
	updated.ID = current.ID
	updated.CatalogID = current.CatalogID
	updated.CreatedAt = current.CreatedAt
}

// The entity association and DeletedAt are json:"-", so a patch
// roundtrip drops them.
func restoreLog(updated, current *models.Log) {
	updated.ID = current.ID
	updated.CreatedAt = current.CreatedAt
	updated.DeletedAt = current.DeletedAt
	updated.EntityID = current.EntityID
	updated.EntityType = current.EntityType
}

func restoreBundle(updated, current *models.Bundle) {
	updated.ID = current.ID
	updated.CreatedAt = current.CreatedAt
}

// refuseNullActive rejects an entity a patch left without an active flag.
// The column is NOT NULL with a database default, so the write would fail
// deep in the database instead of telling the client what is wrong.
func refuseNullActive(active *bool, title string) error {
	if active != nil {
		return nil
	}

	return common.Error(fiber.StatusUnprocessableEntity, title, "active cannot be null")
}

func validatePatchedEntity(request any, title string) error {
	validationErrors := common.ValidateStruct(request)
	if len(validationErrors) == 0 {
		return nil
	}

	return common.ErrorWithValidationErrors(fiber.StatusUnprocessableEntity, title, validationErrors)
}
