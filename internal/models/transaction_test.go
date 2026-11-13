package models

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/gofiber/utils/v2"
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

func TestResourceWebhookDeletionRollsBack(t *testing.T) {
	loadFixtures(t, "publishers.yml")
	drainEventChan()
	t.Cleanup(drainEventChan)

	var publisher Publisher
	require.NoError(t, db.First(&publisher).Error)
	webhook := Webhook{
		ID: utils.UUIDv4(), URL: "https://example.org/rollback", Secret: "retained-secret",
		EntityType: publisher.TableName(), EntityID: publisher.ID,
	}
	require.NoError(t, db.Create(&webhook).Error)
	t.Cleanup(func() { db.Session(&gorm.Session{SkipHooks: true}).Delete(&Webhook{ID: webhook.ID}) })
	drainEventChan()

	errRollback := errors.New("rollback after deletion")
	err := Transaction(db, func(tran *gorm.DB) error {
		if err := tran.Delete(&publisher).Error; err != nil {
			return err
		}

		var count int64
		require.NoError(t, tran.Model(&Webhook{}).Where("id = ?", webhook.ID).Count(&count).Error)
		assert.Zero(t, count)

		return errRollback
	})
	require.ErrorIs(t, err, errRollback)

	var retained Webhook
	require.NoError(t, db.First(&retained, "id = ?", webhook.ID).Error)
	assert.Equal(t, webhook.Secret, retained.Secret)
	require.NoError(t, db.First(&publisher, "id = ?", publisher.ID).Error)
	assert.Empty(t, EventChan)
}

func TestResourceDeletionRetainsFinalRecipients(t *testing.T) {
	loadFixtures(t, "publishers.yml")
	drainEventChan()
	t.Cleanup(drainEventChan)

	var publisher Publisher
	require.NoError(t, db.First(&publisher).Error)
	webhook := Webhook{
		ID: utils.UUIDv4(), URL: "https://example.org/final", Secret: "final-secret",
		EntityType: publisher.TableName(), EntityID: publisher.ID,
	}
	require.NoError(t, db.Create(&webhook).Error)
	drainEventChan()

	require.NoError(t, Transaction(db, func(tran *gorm.DB) error {
		return tran.Delete(&publisher).Error
	}))

	select {
	case event := <-EventChan:
		assert.Equal(t, common.EventTypeDelete, event.Type)
		assert.Equal(t, publisher.ID, event.EntityID)
		require.Len(t, event.DeletedWebhooks, 1)
		assert.Equal(t, webhook.ID, event.DeletedWebhooks[0].ID)
		assert.Equal(t, webhook.Secret, event.DeletedWebhooks[0].Secret)
		body, err := json.Marshal(event)
		require.NoError(t, err)
		assert.NotContains(t, string(body), webhook.Secret)
		assert.NotContains(t, string(body), webhook.URL)
	default:
		t.Fatal("missing final delete event")
	}
}
