package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/ansrivas/fiberprometheus/v2"
	"github.com/caarlos0/env/v6"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cache"
	"github.com/gofiber/fiber/v2/middleware/healthcheck"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/publiccodeyml/open-catalog-api/cmd"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/publiccodeyml/open-catalog-api/internal/database"
	"github.com/publiccodeyml/open-catalog-api/internal/handlers"
	"github.com/publiccodeyml/open-catalog-api/internal/jsondecoder"
	"github.com/publiccodeyml/open-catalog-api/internal/middleware"
	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"github.com/publiccodeyml/open-catalog-api/internal/webhooks"
	"github.com/spf13/cobra"
	"gorm.io/gorm"
)

// The retention window is configured in days, so a shorter cadence
// would find nothing new to delete.
const eventPurgeInterval = 24 * time.Hour

func main() {
	rootCmd := &cobra.Command{
		Use:          "open-catalog-api",
		SilenceUsage: true,
		RunE: func(_ *cobra.Command, _ []string) error {
			app, debouncer, stopEventPurge, _ := Setup()

			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

			go func() {
				<-sigCh

				if err := app.Shutdown(); err != nil {
					log.Printf("graceful shutdown failed: %s", err)
				}
			}()

			err := app.Listen(":3000")

			// Drain runs after Listen returns so any webhook event held
			// in the debouncer's pending window is dispatched before the
			// process exits.
			debouncer.Drain()
			stopEventPurge()

			if err != nil {
				return fmt.Errorf("listen: %w", err)
			}

			return nil
		},
	}

	rootCmd.AddCommand(cmd.NewTokenCmd())

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// Setup builds the app. It also returns the gorm handle: the tests hook
// callbacks on the same connection the handlers use.
func Setup() (*fiber.App, *webhooks.Debouncer, func(), *gorm.DB) {
	if err := env.Parse(&common.EnvironmentConfig); err != nil {
		panic(err)
	}

	gormDB, err := database.NewDatabase(common.EnvironmentConfig.Database)
	if err != nil {
		panic(err)
	}

	// The fixtures carry events far older than any retention window, so
	// under the test harness the purge would delete them.
	eventRetention := time.Duration(common.EnvironmentConfig.EventRetentionDays) * 24 * time.Hour
	if common.EnvironmentConfig.IsTest() {
		eventRetention = 0
	}

	stopEventPurge := database.StartEventPurge(gormDB, eventRetention, eventPurgeInterval)

	// Setup a goroutine acting as a worker for events sent to the
	// EventChan channel.
	//
	// It dispatches the webhooks related to the event that occurred
	// (es. Publisher creation, Software delete, etc.)
	debouncer := webhooks.NewDebouncer(
		time.Duration(common.EnvironmentConfig.WebhookDebounceMS)*time.Millisecond,
		time.Duration(common.EnvironmentConfig.WebhookDebounceMaxMS)*time.Millisecond,
		func(event models.Event) {
			if err := webhooks.DispatchWebhooks(event, gormDB); err != nil {
				log.Println(err)
			}
		},
	)

	go func() {
		for event := range models.EventChan {
			debouncer.Submit(event)
		}
	}()

	app := fiber.New(fiber.Config{
		ErrorHandler: common.CustomErrorHandler,
		// Fiber doesn't set DisallowUnknownFields by default
		// (https://github.com/gofiber/fiber/issues/2601)
		JSONDecoder: jsondecoder.UnmarshalDisallowUnknownFields,

		// With the check on and no trusted proxy configured, Fiber ignores
		// X-Forwarded-For and c.IP() is the peer of the connection, so a
		// client can't pick the address it is rate limited by.
		ProxyHeader:             fiber.HeaderXForwardedFor,
		EnableTrustedProxyCheck: true,
		TrustedProxies:          common.EnvironmentConfig.TrustedProxies,
		// Without validation c.IP() is the whole header value, so a client
		// behind a trusted proxy could still change it on every request.
		EnableIPValidation: true,
	})

	// Automatically recover panics in handlers
	app.Use(recover.New())

	app.Use(healthcheck.New(healthcheck.Config{
		ReadinessProbe: func(c *fiber.Ctx) bool {
			return gormDB.Exec("SELECT 1").Error == nil
		},
	}))

	// Use Fiber Rate API Limiter
	if !common.EnvironmentConfig.IsTest() && common.EnvironmentConfig.MaxRequests != 0 {
		app.Use(limiter.New(limiter.Config{
			Max:               common.EnvironmentConfig.MaxRequests,
			LimiterMiddleware: limiter.SlidingWindow{},
			KeyGenerator: func(ctx *fiber.Ctx) string {
				return ctx.IP()
			},
		}))
	}

	if common.EnvironmentConfig.PasetoKey == nil {
		log.Printf("PASETO_KEY not set, API will run in read-only mode")

		common.EnvironmentConfig.PasetoKey = middleware.NewRandomPasetoKey()
	}

	prometheus := fiberprometheus.New(os.Args[0])
	prometheus.RegisterAt(app, "/metrics")
	app.Use(prometheus.Middleware)

	app.Use(middleware.NewPasetoMiddleware(common.EnvironmentConfig, requiresAuth))

	app.Use(newResponseCache())

	setupHandlers(app, gormDB)

	return app, debouncer, stopEventPurge, gormDB
}

// requiresAuth reports whether a request must carry a valid token. Reads
// are public except for webhook configuration and the audit trail, whose
// GET operations are marked as authenticated in the OpenAPI contract.
func requiresAuth(method, requestPath string) bool {
	if method != fiber.MethodGet {
		return true
	}

	normalizedPath := "/" + strings.Trim(requestPath, "/")
	for _, pattern := range [...]string{
		"/v1/events",
		"/v1/events/*",
		"/v1/webhooks/*",
		"/v1/software/webhooks",
		"/v1/software/*/webhooks",
		"/v1/publishers/webhooks",
		"/v1/publishers/*/webhooks",
	} {
		if matched, _ := path.Match(pattern, normalizedPath); matched {
			return true
		}
	}

	return false
}

func newResponseCache() fiber.Handler {
	return cache.New(cache.Config{
		Next: func(ctx *fiber.Ctx) bool {
			// Don't cache health checks or authenticated resources.
			return ctx.Path() == "/v1/status" ||
				requiresAuth(ctx.Method(), ctx.Path())
		},
		Methods:      []string{fiber.MethodGet, fiber.MethodHead},
		CacheControl: true,
		Expiration:   10 * time.Second,
		KeyGenerator: func(ctx *fiber.Ctx) string {
			return ctx.Path() + string(ctx.Context().QueryArgs().QueryString())
		},
	})
}

func setupHandlers(app *fiber.App, gormDB *gorm.DB) { //nolint:funlen
	bundleHandler := handlers.NewBundle(gormDB)
	catalogHandler := handlers.NewCatalog(gormDB)
	publisherHandler := handlers.NewPublisher(gormDB)
	softwareHandler := handlers.NewSoftware(gormDB)
	statusHandler := handlers.NewStatus(gormDB)
	logHandler := handlers.NewLog(gormDB)
	eventHandler := handlers.NewEvent(gormDB)
	publisherWebhookHandler := handlers.NewWebhook[models.Publisher](gormDB)
	softwareWebhookHandler := handlers.NewWebhook[models.Software](gormDB)

	//nolint:varnamelen
	v1 := app.Group("/v1")

	v1.Get("/bundles", bundleHandler.GetBundles)
	v1.Post("/bundles", bundleHandler.PostBundle)
	v1.Get("/bundles/:id", bundleHandler.GetBundle)
	v1.Patch("/bundles/:id", bundleHandler.PatchBundle)
	v1.Delete("/bundles/:id", bundleHandler.DeleteBundle)

	v1.Get("/catalogs", catalogHandler.GetCatalogs)
	v1.Post("/catalogs", catalogHandler.PostCatalog)
	v1.Get("/catalogs/:id", catalogHandler.GetCatalog)
	v1.Patch("/catalogs/:id", catalogHandler.PatchCatalog)
	v1.Delete("/catalogs/:id", catalogHandler.DeleteCatalog)
	v1.Get("/catalogs/:id/publishers", catalogHandler.GetCatalogPublishers)
	v1.Post("/catalogs/:id/publishers", catalogHandler.PostCatalogPublisher)
	v1.Patch("/catalogs/:id/publishers/:publisherId", catalogHandler.PatchCatalogPublisher)
	v1.Get("/catalogs/:id/software", catalogHandler.GetCatalogSoftware)
	v1.Post("/catalogs/:id/software", catalogHandler.PostCatalogSoftware)
	v1.Patch("/catalogs/:id/software/:softwareId", catalogHandler.PatchCatalogSoftware)
	v1.Get("/catalogs/:id/analysis", catalogHandler.GetCatalogAnalysis)
	v1.Patch("/catalogs/:id/analysis", catalogHandler.PatchCatalogAnalysis)
	v1.Post("/catalogs/:id/logs", logHandler.PostCatalogLog)

	v1.Get("/publishers/webhooks", publisherWebhookHandler.GetResourceWebhooks)
	v1.Post("/publishers/webhooks", publisherWebhookHandler.PostResourceWebhook)
	v1.Get("/publishers/:id/webhooks", publisherWebhookHandler.GetSingleResourceWebhooks)
	v1.Post("/publishers/:id/webhooks", publisherWebhookHandler.PostSingleResourceWebhook)
	v1.Get("/publishers", publisherHandler.GetPublishers)
	v1.Get("/publishers/:id", publisherHandler.GetPublisher)
	v1.Post("/publishers", publisherHandler.PostPublisher)
	v1.Patch("/publishers/:id", publisherHandler.PatchPublisher)
	v1.Delete("/publishers/:id", publisherHandler.DeletePublisher)
	v1.Get("/publishers/:id/logs", logHandler.GetPublisherLogs)
	v1.Post("/publishers/:id/logs", logHandler.PostPublisherLog)

	v1.Get("/software/webhooks", softwareWebhookHandler.GetResourceWebhooks)
	v1.Post("/software/webhooks", softwareWebhookHandler.PostResourceWebhook)
	v1.Get("/software/:id/webhooks", softwareWebhookHandler.GetSingleResourceWebhooks)
	v1.Post("/software/:id/webhooks", softwareWebhookHandler.PostSingleResourceWebhook)
	v1.Get("/software", softwareHandler.GetAllSoftware)
	v1.Get("/software/:id", softwareHandler.GetSoftware)
	v1.Post("/software", softwareHandler.PostSoftware)
	v1.Patch("/software/:id", softwareHandler.PatchSoftware)
	v1.Delete("/software/:id", softwareHandler.DeleteSoftware)
	v1.Get("/software/:id/analysis", softwareHandler.GetSoftwareAnalysis)
	v1.Patch("/software/:id/analysis", softwareHandler.PatchSoftwareAnalysis)

	v1.Get("/logs", logHandler.GetLogs)
	v1.Get("/logs/:id<guid>", logHandler.GetLog)
	v1.Post("/logs", logHandler.PostLog)
	v1.Patch("/logs/:id<guid>", logHandler.PatchLog)
	v1.Delete("/logs/:id<guid>", logHandler.DeleteLog)
	v1.Get("/software/:id/logs", logHandler.GetSoftwareLogs)
	v1.Post("/software/:id/logs", logHandler.PostSoftwareLog)

	v1.Get("/events", eventHandler.GetEvents)
	v1.Get("/events/:id", eventHandler.GetEvent)

	v1.Get("/status", statusHandler.GetStatus)

	v1.Get("/webhooks/:id<guid>", publisherWebhookHandler.GetWebhook)
	v1.Delete("/webhooks/:id<guid>", publisherWebhookHandler.DeleteWebhook)
}
