package models

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func drainEventChan() {
	for {
		select {
		case <-EventChan:
		default:
			return
		}
	}
}

func TestEventChanKeepsEventsFromConcurrentCreates(t *testing.T) {
	loadFixtures(t, "publishers.yml")

	const burst = 50

	drainEventChan()
	t.Cleanup(drainEventChan)

	received := make([]string, 0, burst)
	done := make(chan struct{})

	go func() {
		defer close(done)

		for len(received) < burst {
			select {
			case event := <-EventChan:
				// The dispatch worker sits inside the debouncer between
				// receives, so it is off the channel most of the time.
				time.Sleep(time.Millisecond)

				received = append(received, event.ID)
			case <-time.After(10 * time.Second):
				return
			}
		}
	}()

	var wgroup sync.WaitGroup

	for i := range burst {
		wgroup.Add(1)

		go func() {
			defer wgroup.Done()

			err := db.Create(
				&Publisher{
					ID:          utils.UUIDv4(),
					Description: fmt.Sprintf("Burst publisher %d", i),
				},
			).Error
			assert.NoError(t, err)
		}()
	}

	wgroup.Wait()
	<-done

	assert.Len(t, received, burst)
}

func TestEventChanLogsTheDropWhenFull(t *testing.T) {
	drainEventChan()
	t.Cleanup(drainEventChan)

	for range cap(EventChan) {
		EventChan <- Event{ID: utils.UUIDv4(), Type: common.EventTypeCreate}
	}

	var logged bytes.Buffer

	log.SetOutput(&logged)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	dropped := Event{
		ID:         utils.UUIDv4(),
		Type:       common.EventTypeDelete,
		EntityType: "publishers",
		EntityID:   utils.UUIDv4(),
	}

	returned := make(chan struct{})

	go func() {
		defer close(returned)

		sendNonBlock(dropped)
	}()

	select {
	case <-returned:
	case <-time.After(30 * time.Second):
		require.FailNow(t, "sendNonBlock blocked on a full channel")
	}

	assert.Len(t, EventChan, cap(EventChan))

	assert.Contains(t, logged.String(), dropped.ID)
	assert.Contains(t, logged.String(), dropped.Type)
}
