package webhooks

import (
	"fmt"
	"io"
	"runtime/pprof"
	"sync"
	"testing"
	"time"

	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The goroutineleak profile flags goroutines blocked on primitives no
// runnable goroutine can reach. The detection pass runs when the
// profile is written, so WriteTo comes before Count.
func TestDebouncerLeaksNoGoroutines(t *testing.T) {
	const events = 20

	var wg sync.WaitGroup

	wg.Add(events)

	debouncer := NewDebouncer(time.Millisecond, 10*time.Millisecond, func(models.Event) {
		wg.Done()
	})

	for i := range events {
		debouncer.Submit(models.Event{
			EntityType: "software",
			EntityID:   fmt.Sprintf("id-%d", i),
			Type:       "update",
		})
	}

	wg.Wait()
	debouncer.Drain()

	profile := pprof.Lookup("goroutineleak")
	require.NotNil(t, profile)
	require.NoError(t, profile.WriteTo(io.Discard, 0))

	assert.Zero(t, profile.Count())
}
