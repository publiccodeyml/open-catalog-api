package models

import (
	"log"

	"github.com/gofiber/fiber/v2/utils"
	"github.com/publiccodeyml/open-catalog-api/internal/common"
	"gorm.io/gorm"
)

// The buffer holds events sent while the dispatch worker is busy, so
// they are not dropped. The event row is saved either way, the channel
// only drives the live webhook delivery. 1024 is arbitrary.
const eventChanBuffer = 1024

var EventChan = make(chan Event, eventChanBuffer) //nolint:gochecknoglobals

func (p Publisher) AfterCreate(trx *gorm.DB) error {
	event := Event{
		ID:         utils.UUIDv4(),
		Type:       common.EventTypeCreate,
		EntityType: p.TableName(),
		EntityID:   p.UUID(),
	}

	if err := trx.Create(&event).Error; err != nil {
		return err
	}

	sendNonBlock(event)

	return nil
}

func (s Software) AfterCreate(trx *gorm.DB) error {
	event := Event{
		ID:         utils.UUIDv4(),
		Type:       common.EventTypeCreate,
		EntityType: s.TableName(),
		EntityID:   s.UUID(),
	}

	if err := trx.Create(&event).Error; err != nil {
		return err
	}

	sendNonBlock(event)

	return nil
}

func (p Publisher) AfterUpdate(trx *gorm.DB) error {
	event := Event{
		ID:         utils.UUIDv4(),
		Type:       common.EventTypeUpdate,
		EntityType: p.TableName(),
		EntityID:   p.UUID(),
	}

	if err := trx.Create(&event).Error; err != nil {
		return err
	}

	sendNonBlock(event)

	return nil
}

func (s Software) AfterUpdate(trx *gorm.DB) error {
	event := Event{
		ID:         utils.UUIDv4(),
		Type:       common.EventTypeUpdate,
		EntityType: s.TableName(),
		EntityID:   s.UUID(),
	}

	if err := trx.Create(&event).Error; err != nil {
		return err
	}

	sendNonBlock(event)

	return nil
}

func (p Publisher) AfterDelete(trx *gorm.DB) error {
	event := Event{
		ID:         utils.UUIDv4(),
		Type:       common.EventTypeDelete,
		EntityType: p.TableName(),
		EntityID:   p.UUID(),
	}

	if err := trx.Create(&event).Error; err != nil {
		return err
	}

	sendNonBlock(event)

	return nil
}

func (s Software) AfterDelete(trx *gorm.DB) error {
	event := Event{
		ID:         utils.UUIDv4(),
		Type:       common.EventTypeDelete,
		EntityType: s.TableName(),
		EntityID:   s.UUID(),
	}

	if err := trx.Create(&event).Error; err != nil {
		return err
	}

	sendNonBlock(event)

	return nil
}

func sendNonBlock(event Event) {
	select {
	case EventChan <- event:
	default:
		log.Printf("event channel full, dropping the live dispatch of event %s (%s)\n", event.ID, event.Type)
	}
}
