package models

import (
	"errors"
	"testing"

	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func countEvents(t *testing.T, entityID string) int64 {
	t.Helper()

	var count int64

	require.NoError(t, db.Model(&Event{}).Where("entity_id = ?", entityID).Count(&count).Error)

	return count
}

func TestTransactionDropsTheEventOfARolledBackWrite(t *testing.T) {
	drainEventChan()
	t.Cleanup(drainEventChan)

	publisherID := utils.UUIDv4()
	errRollback := errors.New("rolled back on purpose")

	err := Transaction(db, func(tran *gorm.DB) error {
		if err := tran.Create(
			&Publisher{ID: publisherID, Description: "Rolled back publisher"},
		).Error; err != nil {
			return err
		}

		return errRollback
	})

	assert.ErrorIs(t, err, errRollback)

	assert.Zero(t, countEvents(t, publisherID))
	assert.Len(t, EventChan, 0)
}

func TestTransactionSendsTheEventOnlyAfterTheCommit(t *testing.T) {
	drainEventChan()
	t.Cleanup(drainEventChan)

	publisherID := utils.UUIDv4()

	err := Transaction(db, func(tran *gorm.DB) error {
		if err := tran.Create(
			&Publisher{ID: publisherID, Description: "Committed publisher"},
		).Error; err != nil {
			return err
		}

		assert.Len(t, EventChan, 0, "the event reached the channel before the commit")

		return nil
	})
	require.NoError(t, err)

	assert.Equal(t, int64(1), countEvents(t, publisherID))

	require.Len(t, EventChan, 1)

	event := <-EventChan
	assert.Equal(t, publisherID, event.EntityID)
	assert.Equal(t, common.EventTypeCreate, event.Type)
}
