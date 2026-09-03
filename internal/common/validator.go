package common

import (
	"context"
	"errors"
	"net"
	"net/url"
	"reflect"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/publiccodeyml/open-catalog-api/internal/jsondecoder"
)

// hostValidator runs the `fqdn` tag against the host of a candidate URL.
// Kept as a separate instance so we don't recurse into the struct-level
// validator we are already running.
//
//nolint:gochecknoglobals // shared validator instance for the host check
var hostValidator = validator.New()

const (
	tagPosition      = 2
	maxProvidedValue = 255

	// A webhook URL is validated while a request is being served, so the
	// resolution has to give up quickly if the resolver is slow.
	webhookLookupTimeout = 2 * time.Second
)

type ValidationError struct {
	Field string `json:"field"`
	Rule  string `json:"rule"`
	Value string `json:"value"`
}

// IsForbiddenIP reports whether ip must not be dialed by the webhook
// dispatcher. IsPrivate covers RFC 1918 and RFC 4193.
func IsForbiddenIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() ||
		ip.IsPrivate()
}

// validateGitHubWebhook requires a github format webhook to target the
// repository_dispatch endpoint, the one URL its payload is valid for.
// Anything else would receive the bearer secret for nothing.
func validateGitHubWebhook(level validator.StructLevel) {
	webhook, ok := reflect.TypeAssert[Webhook](level.Current())
	if !ok || webhook.Format != WebhookFormatGitHub {
		return
	}

	parsed, err := url.Parse(webhook.URL)
	if err != nil {
		level.ReportError(webhook.URL, "url", "URL", "github_api_url", "")

		return
	}

	parts := strings.Split(parsed.Path, "/")
	dispatchPath := len(parts) == 5 &&
		parts[1] == "repos" && parts[2] != "" && parts[3] != "" &&
		parts[4] == "dispatches"

	if parsed.Hostname() != "api.github.com" || !dispatchPath {
		level.ReportError(webhook.URL, "url", "URL", "github_api_url", "")
	}
}

// validateWebhookURL rejects URLs that are not HTTPS, contain userinfo, or
// resolve to a private/loopback/link-local address.
func validateWebhookURL(fl validator.FieldLevel) bool {
	parsed, err := url.Parse(fl.Field().String())
	if err != nil {
		return false
	}

	if parsed.Scheme != "https" {
		return false
	}

	if parsed.User != nil {
		return false
	}

	host := parsed.Hostname()
	if host == "" {
		return false
	}

	if ip := net.ParseIP(host); ip != nil {
		return !IsForbiddenIP(ip)
	}

	ctx, cancel := context.WithTimeout(context.Background(), webhookLookupTimeout)
	defer cancel()

	// Resolution is best effort at registration time, so a name that does not
	// resolve yet is accepted. The dispatcher checks again before every send.
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return true
	}

	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil && IsForbiddenIP(ip) {
			return false
		}
	}

	return true
}

func ValidateStruct(validateStruct any) []ValidationError {
	validate := validator.New()
	// https://github.com/go-playground/validator/issues/258#issuecomment-257281334
	validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		return strings.SplitN(fld.Tag.Get("json"), ",", tagPosition)[0]
	})

	_ = validate.RegisterValidation("code_hosting_url", validateCodeHostingURL)
	_ = validate.RegisterValidation("webhook_url", validateWebhookURL)
	validate.RegisterStructValidation(validateGitHubWebhook, Webhook{})

	var validationErrors []ValidationError

	if err := validate.Struct(validateStruct); err != nil {
		ve, ok := errors.AsType[validator.ValidationErrors](err)
		if !ok {
			return nil
		}

		for _, err := range ve {
			var value string

			value, ok := err.Value().(string)

			if !ok {
				value = ""
			}

			valueRunes := []rune(value)
			if len(valueRunes) > maxProvidedValue {
				value = string(valueRunes[:maxProvidedValue])
			}

			validationErrors = append(validationErrors, ValidationError{
				Field: err.Field(),
				Rule:  err.Tag(),
				Value: value,
			})
		}
	}

	return validationErrors
}

func ValidateRequestEntity(ctx *fiber.Ctx, request any, errorMessage string) error {
	if err := ctx.BodyParser(request); err != nil {
		if errors.Is(err, jsondecoder.ErrUnknownField) {
			return Error(fiber.StatusUnprocessableEntity, errorMessage, err.Error())
		}

		return Error(fiber.StatusBadRequest, errorMessage, "invalid or malformed JSON")
	}

	if err := ValidateStruct(request); err != nil {
		return ErrorWithValidationErrors(
			fiber.StatusUnprocessableEntity, errorMessage, err,
		)
	}

	return nil
}

// validateCodeHostingURL rejects publisher CodeHosting URLs that point at
// non public hosts. The "http_url" tag already vets the scheme and overall
// shape. Here we only require the host to be a real FQDN, which
// excludes IP literals and single label hosts.
func validateCodeHostingURL(fl validator.FieldLevel) bool {
	parsed, err := url.Parse(fl.Field().String())
	if err != nil {
		return false
	}

	return hostValidator.Var(parsed.Hostname(), "fqdn") == nil
}

func GenerateErrorDetails(validationErrors []ValidationError) string {
	var errors []string

	for _, validationError := range validationErrors {
		switch validationError.Rule {
		case "required":
			errors = append(errors, validationError.Field+" is required")
		case "email":
			errors = append(errors, validationError.Field+" is not a valid email")
		case "min":
			errors = append(errors, validationError.Field+" does not meet its size limits (too short)")
		case "max":
			errors = append(errors, validationError.Field+" does not meet its size limits (too long)")
		case "gt":
			errors = append(errors, validationError.Field+" does not meet its size limits (too few items)")
		case "code_hosting_url":
			errors = append(errors, validationError.Field+" is not a valid public http(s) URL")
		case "github_api_url":
			errors = append(errors, validationError.Field+
				" must be https://api.github.com/repos/{owner}/{repo}/dispatches for the github format")
		default:
			errors = append(errors, validationError.Field+" is invalid")
		}
	}

	errorDetails := "invalid format: " + strings.Join(errors, ", ")

	return errorDetails
}
