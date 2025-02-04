package chat

import (
	"errors"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type consensusStore struct {
	sync.Mutex

	groups map[uuid.UUID]*canonicalStack
}

func (b *bounce) reloadGroupConsensus(groupID uuid.UUID) {
	b.consensusStore.Lock()
	defer b.consensusStore.Unlock()

	stack, _ := b.buildCanonicalHistoryStack(groupID)
	b.consensusStore.groups[groupID] = stack
}

func (b *bounce) reloadGroupConsensusSince(groupID uuid.UUID, ts int64) {
	// Reload everything if timestamp is 0
	if ts == 0 {
		b.reloadGroupConsensus(groupID)
		return
	}

	// Find the stack, reload everything if we don't have a stack yet
	b.consensusStore.Lock()
	stack, ok := b.consensusStore.groups[groupID]
	b.consensusStore.Unlock()
	if !ok {
		b.reloadGroupConsensus(groupID)
		return
	}

	// Remove updates that are at or older than timestamp from the stack
	untouchedState := []groupState{}
	for _, gs := range stack.history {
		if gs.ug.Timestamp < ts {
			untouchedState = append(untouchedState, gs)
		} else {
			break
		}
	}
	stack.history = untouchedState

	// Load all updates that are timestamp or newer from the database
	var ugs []updateGroup
	err := b.database.Preload(clause.Associations).Where("target = ? AND timestamp >= ?", groupID, ts).Order("timestamp asc").Find(&ugs).Error
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error selecting new update groups during partial reload")
	}

	// Add all updates from the database to the stack
	for _, ug := range ugs {
		b.insertUpdateGroupIntoStack(stack, ug)
	}
}

func (b *bounce) writeGroupConsensus(groupID uuid.UUID) error {
	var g group
	err := b.database.Preload(clause.Associations).Where("id = ?", groupID).First(&g).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			b.applyUpdateGroupsForNonexistentGroup(groupID)
			return nil
		} else {
			log.WithFields(log.Fields{
				"group_id": groupID,
				"error":    err.Error(),
			}).Fatal("database error looking up group")
		}
	}

	b.consensusStore.Lock()
	stack := b.consensusStore.groups[groupID]
	b.consensusStore.Unlock()
	ugs := []updateGroup{}
	err = b.database.Find(&ugs, "target = ?", groupID).Error
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Fatal("database error looking up update groups")
	}

	err = b.setRollbacksApplicationsAndGroupState(g, stack, ugs)
	return err
}

func (b *bounce) currentGroupState(groupID uuid.UUID) (groupState, error) {
	b.consensusStore.Lock()
	stack, ok := b.consensusStore.groups[groupID]
	b.consensusStore.Unlock()
	if !ok {
		b.reloadGroupConsensus(groupID)
		b.consensusStore.Lock()
		stack, ok = b.consensusStore.groups[groupID]
		b.consensusStore.Unlock()
		if !ok {
			return groupState{}, errors.New("group consensus state doesn't exist after creation")
		}
	}

	top, err := stack.top() //TODO: lock?
	if err != nil {
		return groupState{}, err
	}

	return top, nil
}
