package common

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v3"
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

// IsForbiddenIP reports whether ipAddress must not be dialed by the webhook
// dispatcher. IsPrivate covers RFC 1918 and RFC 4193. RFC 6598 shared
// address space is not publicly routable either.
func IsForbiddenIP(ipAddress net.IP) bool {
	// A NAT64 or 6to4 gateway forwards these addresses to the IPv4 host
	// they carry, so that host has to pass the same checks.
	if embedded := embeddedIPv4(ipAddress); embedded != nil {
		return IsForbiddenIP(embedded)
	}

	ipv4 := ipAddress.To4()

	return ipAddress.IsLoopback() ||
		ipAddress.IsLinkLocalUnicast() ||
		ipAddress.IsLinkLocalMulticast() ||
		ipAddress.IsUnspecified() ||
		ipAddress.IsPrivate() ||
		(ipv4 != nil && ipv4[0] == 100 && ipv4[1]&0xc0 == 0x40)
}

// embeddedIPv4 returns the IPv4 address carried by a NAT64 address in the
// RFC 6052 well-known prefix 64:ff9b::/96 or by a RFC 3056 6to4 address in
// 2002::/16, or nil for any other address.
func embeddedIPv4(ipAddress net.IP) net.IP {
	ipv6 := ipAddress.To16()
	if ipv6 == nil || ipAddress.To4() != nil {
		return nil
	}

	nat64Prefix := []byte{0, 0x64, 0xff, 0x9b, 0, 0, 0, 0, 0, 0, 0, 0}
	if bytes.Equal(ipv6[:len(nat64Prefix)], nat64Prefix) {
		return net.IPv4(ipv6[12], ipv6[13], ipv6[14], ipv6[15])
	}

	if ipv6[0] == 0x20 && ipv6[1] == 0x02 {
		return net.IPv4(ipv6[2], ipv6[3], ipv6[4], ipv6[5])
	}

	return nil
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

// requestValidator is built once: a Validate instance caches the parsed
// tags of every struct it sees and is safe for concurrent use, so a new
// one per request parsed the same tags again on every call.
//
//nolint:gochecknoglobals // one shared instance is the point
var requestValidator = sync.OnceValue(newRequestValidator)

func newRequestValidator() *validator.Validate {
	validate := validator.New()
	// https://github.com/go-playground/validator/issues/258#issuecomment-257281334
	validate.RegisterTagNameFunc(func(fld reflect.StructField) string {
		return strings.SplitN(fld.Tag.Get("json"), ",", tagPosition)[0]
	})

	_ = validate.RegisterValidation("code_hosting_url", validateCodeHostingURL)
	_ = validate.RegisterValidation("webhook_url", validateWebhookURL)
	validate.RegisterStructValidation(validateGitHubWebhook, Webhook{})

	return validate
}

func ValidateStruct(validateStruct any) []ValidationError {
	var validationErrors []ValidationError

	if err := requestValidator().Struct(validateStruct); err != nil {
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

func ValidateRequestEntity(ctx fiber.Ctx, request any, errorMessage string) error {
	if err := ctx.Bind().Body(request); err != nil {
		// Fiber wraps the decoder error with the binding source, which
		// would leak into the detail.
		if bindErr, ok := errors.AsType[*fiber.BindError](err); ok {
			err = bindErr.Err
		}

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
