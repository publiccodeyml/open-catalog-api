package common

import (
	"strings"

	"github.com/PuerkitoBio/purell"
)

const normalizeURLFlags = purell.FlagsUsuallySafeGreedy | purell.FlagRemoveWWW

func NormalizeURL(rawURL string) string {
	normalized, err := purell.NormalizeURLString(rawURL, normalizeURLFlags)
	if err != nil {
		return rawURL
	}

	return normalized
}

type SourceInput struct {
	Driver *string  `json:"driver" validate:"omitempty,min=1,max=64"`
	URL    string   `json:"url" validate:"required,url,max=2048"`
	Args   []string `json:"args" validate:"omitempty,min=1,max=20,dive,min=1,max=2048"`
}

type CatalogPost struct {
	Name                string        `json:"name" validate:"required,min=1,max=255"`
	AlternativeID       *string       `json:"alternativeId" validate:"omitempty,min=1,max=255"`
	Active              *bool         `json:"active"`
	PublishersNamespace *string       `json:"publishersNamespace" validate:"omitempty,max=255"`
	Scopes              []string      `json:"scopes" validate:"omitempty,max=20,dive,min=1,max=64"`
	Sources             []SourceInput `json:"sources" validate:"omitempty,max=100,dive"`
}

type CatalogPatch struct {
	Name                *string        `json:"name" validate:"omitempty,min=1,max=255"`
	AlternativeID       *string        `json:"alternativeId" validate:"omitempty,max=255"`
	Active              *bool          `json:"active"`
	PublishersNamespace *string        `json:"publishersNamespace" validate:"omitempty,max=255"`
	Scopes              *[]string      `json:"scopes" validate:"omitempty,max=20,dive,min=1,max=64"`
	Sources             *[]SourceInput `json:"sources" validate:"omitempty,gt=0,max=100,dive"`
}

type PublisherPost struct {
	CodeHosting   []CodeHosting `json:"codeHosting" validate:"required,gt=0,max=255,dive"`
	Description   string        `json:"description" validate:"required,max=255"`
	Email         *string       `json:"email" validate:"omitempty,max=255,email"`
	Active        *bool         `json:"active"`
	AlternativeID *string       `json:"alternativeId" validate:"omitempty,min=1,max=255"`
}

type PublisherPatch struct {
	CodeHosting   *[]CodeHosting `json:"codeHosting" validate:"omitempty,gt=0,max=255,dive"`
	Description   *string        `json:"description" validate:"omitempty,max=255"`
	Email         *string        `json:"email" validate:"omitempty,max=255,email"`
	Active        *bool          `json:"active"`
	AlternativeID *string        `json:"alternativeId" validate:"omitempty,max=255"`
}

type CodeHosting struct {
	URL   string `json:"url" validate:"required,max=255,http_url,code_hosting_url"`
	Group *bool  `json:"group"`
}

type SoftwarePost struct {
	URL           string   `json:"url" validate:"required,max=255,url"`
	Aliases       []string `json:"aliases" validate:"max=255,dive,max=255,url"`
	PubliccodeYml string   `json:"publiccodeYml" validate:"required,max=99999"`
	Active        *bool    `json:"active"`
	Vitality      *string  `json:"vitality" validate:"omitempty,max=99999"`
}

type SoftwarePatch struct {
	URL           *string   `json:"url" validate:"omitempty,max=255,url"`
	Aliases       *[]string `json:"aliases" validate:"omitempty,max=255,dive,max=255,url"`
	PubliccodeYml *string   `json:"publiccodeYml" validate:"omitempty,max=99999"`
	Active        *bool     `json:"active"`
	Vitality      *string   `json:"vitality" validate:"omitempty,max=99999"`
}

type BundlePost struct {
	Name        string   `json:"name" validate:"required,min=1,max=255"`
	Description *string  `json:"description" validate:"omitempty,max=1000"`
	Active      *bool    `json:"active"`
	SoftwareIDs []string `json:"softwareIds" validate:"required,gt=0,unique,dive,uuid4"`
}

type BundlePatch struct {
	Name        *string   `json:"name" validate:"omitempty,min=1,max=255"`
	Description *string   `json:"description" validate:"omitempty,max=1000"`
	Active      *bool     `json:"active"`
	SoftwareIDs *[]string `json:"softwareIds" validate:"omitempty,gt=0,unique,dive,uuid4"`
}

type Log struct {
	Message string `json:"message" validate:"required,max=2048"`
}

type Webhook struct {
	URL    string `json:"url" validate:"required,max=255,url,webhook_url"`
	Secret string `json:"secret" validate:"omitempty,min=16,max=256"`
	Format string `json:"format" validate:"omitempty,oneof=default github standard-webhooks"`
}

func NormalizeEmail(email *string) *string {
	if email == nil {
		return nil
	}

	normalized := strings.TrimSpace(strings.ToLower(*email))

	return &normalized
}
