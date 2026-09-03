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

func validatePatchedBundle(bundle models.Bundle, title string) error {
	request := common.BundlePatch{
		Name:        &bundle.Name,
		Description: bundle.Description,
		Active:      bundle.Active,
		SoftwareIDs: &bundle.SoftwareIDs,
	}

	return validatePatchedEntity(request, title)
}

func validatePatchedEntity(request any, title string) error {
	validationErrors := common.ValidateStruct(request)
	if len(validationErrors) == 0 {
		return nil
	}

	return common.ErrorWithValidationErrors(fiber.StatusUnprocessableEntity, title, validationErrors)
}
