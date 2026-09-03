package database

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/publiccodeyml/open-catalog-api/internal/models"
	"gorm.io/gorm"
)

// PurgeEvents deletes the events created more than olderThan ago and
// returns how many rows went away.
func PurgeEvents(gormdb *gorm.DB, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().Add(-olderThan)

	result := gormdb.Where("created_at < ?", cutoff).Delete(&models.Event{})
	if result.Error != nil {
		return 0, fmt.Errorf("can't purge the events older than %s: %w", olderThan, result.Error)
	}

	return result.RowsAffected, nil
}

// StartEventPurge purges the events once and then every interval, until
// the returned function is called. A zero olderThan keeps every event
// and makes both the purge and the returned function a no op.
func StartEventPurge(gormdb *gorm.DB, olderThan, interval time.Duration) func() {
	if olderThan <= 0 || interval <= 0 {
		return func() {}
	}

	done := make(chan struct{})

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			purgeEvents(gormdb, olderThan)

			select {
			case <-ticker.C:
			case <-done:
				return
			}
		}
	}()

	return sync.OnceFunc(func() { close(done) })
}

func purgeEvents(gormdb *gorm.DB, olderThan time.Duration) {
	purged, err := PurgeEvents(gormdb, olderThan)
	if err != nil {
		log.Println(err)

		return
	}

	if purged > 0 {
		log.Printf("purged %d events older than %s", purged, olderThan)
	}
}
