package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/publiccodeyml/open-catalog-api/internal/models"
)

func setupDB(t *testing.T, webhooks []models.Webhook) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)

	require.NoError(t, db.AutoMigrate(&models.Webhook{}))

	for i := range webhooks {
		require.NoError(t, db.Create(&webhooks[i]).Error)
	}

	return db
}

func expectedSignature(secret string, payload []byte) string {
	h := hmac.New(sha256.New, []byte(secret))
	_, _ = h.Write(payload)

	return hex.EncodeToString(h.Sum(nil))
}

// The synctest bubble gives the test a fake clock, so the server delay
// and the timeout cost no real time and elapsed is exact instead of
// bracketed between margins.
func TestPostTimesOut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		original := dispatchTimeout
		dispatchTimeout = 200 * time.Millisecond
		defer func() { dispatchTimeout = original }()

		const serverDelay = 600 * time.Millisecond

		srv := httptest.NewTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(serverDelay)
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		originalClient := httpClient
		httpClient = srv.Client()
		defer func() { httpClient = originalClient }()

		start := time.Now()
		post(srv.URL, []byte(`{"event":"test","subject":"/software"}`), "")
		elapsed := time.Since(start)

		assert.Equal(t, dispatchTimeout, elapsed,
			"post should give up exactly at the dispatch timeout")
	})
}

// TestDispatchWebhooks_PerWebhookSignature verifies that each webhook subscriber
// receives an HMAC signature computed with its own secret, not another's.
func TestDispatchWebhooks_PerWebhookSignature(t *testing.T) {
	var mu sync.Mutex
	received := map[string]string{}

	var wg sync.WaitGroup
	wg.Add(2)

	makeServer := func(name string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			received[name] = r.Header.Get("X-Webhook-Signature")
			mu.Unlock()

			wg.Done()
			w.WriteHeader(http.StatusOK)
		}))
	}

	srv1 := makeServer("srv1")
	defer srv1.Close()

	srv2 := makeServer("srv2")
	defer srv2.Close()

	secret1 := "secret-one"
	secret2 := "secret-two"

	db := setupDB(t, []models.Webhook{
		{ID: "wh-1", URL: srv1.URL, Secret: secret1, EntityType: "software", EntityID: ""},
		{ID: "wh-2", URL: srv2.URL, Secret: secret2, EntityType: "software", EntityID: ""},
	})

	event := models.Event{Type: "created", EntityType: "software", EntityID: ""}

	err := DispatchWebhooks(event, db)
	require.NoError(t, err)

	wg.Wait()

	payload, err := json.Marshal(map[string]string{
		"event":   "created",
		"subject": "/software",
	})
	require.NoError(t, err)

	mu.Lock()
	sig1 := received["srv1"]
	sig2 := received["srv2"]
	mu.Unlock()

	assert.NotEqual(t, sig1, sig2, "signatures must differ per webhook")
	assert.Equal(t, expectedSignature(secret1, payload), sig1, "srv1 signature must match secret1")
	assert.Equal(t, expectedSignature(secret2, payload), sig2, "srv2 signature must match secret2")
}

// TestDispatchWebhooks_EmptySecretNoHeader verifies that when a webhook has no
// secret, the X-Webhook-Signature header is absent from the request.
func TestDispatchWebhooks_EmptySecretNoHeader(t *testing.T) {
	var mu sync.Mutex
	var headerPresent bool

	var wg sync.WaitGroup
	wg.Add(1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		_, headerPresent = r.Header["X-Webhook-Signature"]
		mu.Unlock()

		wg.Done()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	db := setupDB(t, []models.Webhook{
		{ID: "wh-3", URL: srv.URL, Secret: "", EntityType: "software", EntityID: ""},
	})

	event := models.Event{Type: "deleted", EntityType: "software", EntityID: ""}

	err := DispatchWebhooks(event, db)
	require.NoError(t, err)

	wg.Wait()

	mu.Lock()
	present := headerPresent
	mu.Unlock()

	assert.False(t, present, "X-Webhook-Signature header must be absent when secret is empty")
}

// TestDispatchWebhooks_RotatedSecretTakesEffect verifies that a secret rotated
// in the database signs the next dispatch, with no restart and no cache in
// front of the lookup.
func TestDispatchWebhooks_RotatedSecretTakesEffect(t *testing.T) {
	signatures := make(chan string, 2)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signatures <- r.Header.Get("X-Webhook-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	const (
		oldSecret = "secret-before-rotation"
		newSecret = "secret-after-rotation"
	)

	db := setupDB(t, []models.Webhook{
		{ID: "wh-4", URL: srv.URL, Secret: oldSecret, EntityType: "publishers", EntityID: ""},
	})

	event := models.Event{Type: "created", EntityType: "publishers", EntityID: ""}

	require.NoError(t, DispatchWebhooks(event, db))
	before := <-signatures

	require.NoError(t, db.Model(&models.Webhook{}).
		Where("id = ?", "wh-4").
		Update("secret", newSecret).Error)

	require.NoError(t, DispatchWebhooks(event, db))
	after := <-signatures

	payload, err := json.Marshal(map[string]string{
		"event":   "created",
		"subject": "/publishers",
	})
	require.NoError(t, err)

	assert.Equal(t, expectedSignature(oldSecret, payload), before)
	assert.Equal(t, expectedSignature(newSecret, payload), after)
}
