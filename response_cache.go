package main

import (
	"crypto/sha256"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cache"
)

// Clients choose the cache keys, so distinct query strings could
// otherwise grow memory for as long as requests keep coming.
const (
	responseCacheExpiration = 10 * time.Second

	// responseCacheMaxBytes bounds the response bodies the cache keeps.
	responseCacheMaxBytes = 32 << 20

	// responseCacheMaxEntries bounds how many responses the cache keeps,
	// since small bodies alone would let the entries grow without limit.
	responseCacheMaxEntries = 10000

	// responseCacheStorageDelay covers how late the cache storage frees
	// an expired response: its clock and its cleanup both tick once per
	// second.
	responseCacheStorageDelay = 2 * time.Second
)

// newResponseCache doesn't use the cache's MaxBytes: it keeps a record
// of every expired response until byte eviction removes it, so distinct
// query strings would still grow memory. The admission check bounds
// both bytes and entries instead.
func newResponseCache(maxBytes, maxEntries int, now func() time.Time) fiber.Handler {
	admission := newCacheAdmission(maxBytes, maxEntries, responseCacheExpiration+responseCacheStorageDelay)

	return cache.New(cache.Config{
		// The cache calls Next after the handler, only for responses it
		// is about to store.
		Next: func(ctx *fiber.Ctx) bool {
			// Don't cache health checks or authenticated resources.
			if routePath(ctx.Path()) == "/v1/status" || requiresAuth(ctx.Method(), ctx.Path()) {
				return true
			}

			return !admission.admit(now(), len(ctx.Response().Body()))
		},
		Methods:      []string{fiber.MethodGet},
		CacheControl: true,
		Expiration:   responseCacheExpiration,
		KeyGenerator: func(ctx *fiber.Ctx) string {
			// A digest keeps every key the same size however long the URL.
			key := sha256.Sum256([]byte(ctx.Path() + "?" + string(ctx.Context().QueryArgs().QueryString())))

			return string(key[:])
		},
	})
}

type cacheAdmissionEntry struct {
	expiry time.Time
	size   int
}

// cacheAdmission counts the responses stored and their body bytes
// within the expiration window, and refuses a new one that would go
// over either limit. Responses already cached keep being served until
// they expire.
type cacheAdmission struct {
	mu       sync.Mutex
	entries  []cacheAdmissionEntry // ring buffer, oldest at head
	head     int
	count    int
	bytes    int
	maxBytes int
	ttl      time.Duration
}

func newCacheAdmission(maxBytes, maxEntries int, ttl time.Duration) *cacheAdmission {
	return &cacheAdmission{
		entries:  make([]cacheAdmissionEntry, maxEntries),
		maxBytes: maxBytes,
		ttl:      ttl,
	}
}

func (a *cacheAdmission) admit(now time.Time, size int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	for a.count > 0 && !now.Before(a.entries[a.head].expiry) {
		a.bytes -= a.entries[a.head].size
		a.head = (a.head + 1) % len(a.entries)
		a.count--
	}

	if a.count == len(a.entries) || size > a.maxBytes-a.bytes {
		return false
	}

	a.entries[(a.head+a.count)%len(a.entries)] = cacheAdmissionEntry{expiry: now.Add(a.ttl), size: size}
	a.count++
	a.bytes += size

	return true
}
