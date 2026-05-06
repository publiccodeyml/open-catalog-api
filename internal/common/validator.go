package common

import (
	"errors"
	"net"
	"net/url"
	"reflect"
	"strings"

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
)

type ValidationError struct {
	Field string `json:"field"`
	Rule  string `json:"rule"`
	Value string `json:"value"`
}

// privateRanges lists CIDR blocks that must not be reachable via webhooks.
var privateRanges = func() []*net.IPNet {
	cidrs := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"fc00::/7",
	}
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, n, _ := net.ParseCIDR(cidr)
		nets = append(nets, n)
	}
	return nets
}()

// isForbiddenIP reports whether ip must not be dialed by the webhook dispatcher.
func isForbiddenIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	for _, r := range privateRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
}

// validateWebhookURL rejects URLs that are not HTTPS, contain userinfo, or
// resolve to a private/loopback/link-local address.
func validateWebhookURL(fl validator.FieldLevel) bool {
	raw := fl.Field().String()

	u, err := url.Parse(raw)
	if err != nil {
		return false
	}

	if u.Scheme != "https" {
		return false
	}

	if u.User != nil {
		return false
	}

	host := u.Hostname()
	if host == "" {
		return false
	}

	if ip := net.ParseIP(host); ip != nil {
		return !isForbiddenIP(ip)
	}

	// DNS lookup is best effort at registration time; unresolvable hostnames
	// are allowed through. The dispatch-layer dial guard is the real defence.
	addrs, err := net.LookupHost(host)
	if err != nil {
		return true
	}

	for _, addr := range addrs {
		if ip := net.ParseIP(addr); ip != nil && isForbiddenIP(ip) {
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
	//nolint:errcheck // registration only fails if tag is empty or already registered
	_ = validate.RegisterValidation("webhook_url", validateWebhookURL)

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
		default:
			errors = append(errors, validationError.Field+" is invalid")
		}
	}

	errorDetails := "invalid format: " + strings.Join(errors, ", ")

	return errorDetails
}
