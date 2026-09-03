package models

import (
	"context"
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
	return emit(trx, common.EventTypeCreate, p)
}

func (s Software) AfterCreate(trx *gorm.DB) error {
	return emit(trx, common.EventTypeCreate, s)
}

func (p Publisher) AfterUpdate(trx *gorm.DB) error {
	return emit(trx, common.EventTypeUpdate, p)
}

func (s Software) AfterUpdate(trx *gorm.DB) error {
	return emit(trx, common.EventTypeUpdate, s)
}

func (p Publisher) AfterDelete(trx *gorm.DB) error {
	return emit(trx, common.EventTypeDelete, p)
}

func (s Software) AfterDelete(trx *gorm.DB) error {
	return emit(trx, common.EventTypeDelete, s)
}

// pendingEventsKey addresses the collector Transaction leaves in the
// statement context for the hooks running under it.
type pendingEventsKey struct{}

type pendingEvents struct {
	events []Event
}

// Transaction runs fc in a database transaction and hands the events its
// hooks recorded to the live dispatch once the commit succeeded. A write
// that rolls back takes its event row with it, so no receiver is told
// about a change that never happened.
func Transaction(gormdb *gorm.DB, txFunc func(tx *gorm.DB) error) error {
	ctx := context.Background()
	if gormdb.Statement != nil && gormdb.Statement.Context != nil {
		ctx = gormdb.Statement.Context
	}

	pending := &pendingEvents{}

	err := gormdb.WithContext(context.WithValue(ctx, pendingEventsKey{}, pending)).Transaction(txFunc)
	if err != nil {
		return err //nolint:wrapcheck // the handlers map the driver error to a status
	}

	for _, event := range pending.events {
		sendNonBlock(event)
	}

	return nil
}

// emit records an event of the given type for model. It runs inside the
// caller's transaction, so a failed insert rolls the write back. Under
// Transaction the event waits for the commit, a direct write hands it to
// the live dispatch right away.
func emit(trx *gorm.DB, eventType string, model Model) error {
	event := Event{
		ID:         utils.UUIDv4(),
		Type:       eventType,
		EntityType: model.TableName(),
		EntityID:   model.UUID(),
	}

	if trx.Statement != nil {
		event.Actor = common.Actor(trx.Statement.Context)
	}

	if err := trx.Create(&event).Error; err != nil {
		return err
	}

	if pending, ok := pendingFrom(trx); ok {
		pending.events = append(pending.events, event)

		return nil
	}

	sendNonBlock(event)

	return nil
}

func pendingFrom(trx *gorm.DB) (*pendingEvents, bool) {
	if trx.Statement == nil || trx.Statement.Context == nil {
		return nil, false
	}

	pending, ok := trx.Statement.Context.Value(pendingEventsKey{}).(*pendingEvents)

	return pending, ok
}

func sendNonBlock(event Event) {
	select {
	case EventChan <- event:
	default:
		log.Printf("event channel full, dropping the live dispatch of event %s (%s)\n", event.ID, event.Type)
	}
}
