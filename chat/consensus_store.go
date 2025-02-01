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

func (b *bounce) createConsensusStore() {
	if b.consensusStore != nil {
		log.Fatal("cannot create consensus store more than once")
	}

	cs := &consensusStore{
		groups: make(map[uuid.UUID]*canonicalStack),
	}
	cs.Lock()
	defer cs.Unlock()

	var groups []group
	err := b.database.Select("id").Find(&groups).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("database error getting all groups")
	}
	for _, g := range groups {
		stack, _ := b.buildCanonicalHistoryStack(g.ID)
		cs.groups[g.ID] = stack
	}

	b.consensusStore = cs
}

func (b *bounce) addGroupToConsensusStore(groupID uuid.UUID) {
	b.consensusStore.Lock()
	defer b.consensusStore.Unlock()

	stack, _ := b.buildCanonicalHistoryStack(groupID)
	b.consensusStore.groups[groupID] = stack
}

func (cs *consensusStore) postingRestricted(groupID uuid.UUID) bool {
	cs.Lock()
	defer cs.Unlock()

	stack, ok := cs.groups[groupID]
	if !ok {
		log.WithFields(log.Fields{
			"group_id": groupID,
		}).Error("canonical stack not found for group")
		return true
	}

	gs, err := stack.top()
	if err != nil {
		log.WithFields(log.Fields{
			"group_id": groupID,
			"error":    err.Error(),
		}).Error("error getting top of stack for group history while looking up posting restrictions")
		return true
	}

	return gs.postingRestricted
}

func (cs *consensusStore) add(ug updateGroup) {
	cs.Lock()
	defer cs.Unlock()

	stack, ok := cs.groups[ug.Target]
	if !ok {
		log.WithFields(log.Fields{
			"update_group_id": ug.ID,
			"group_id":        ug.Target,
		}).Error("cannot add update group for group not in consensus store")
		return
	}

	newerUgs := []updateGroup{}
	for {
		top, err := stack.top()
		if err != nil {
			log.WithFields(log.Fields{
				"error":           err.Error(),
				"update_group_id": ug.ID,
			}).Error("error getting top of stack when adding update group")
			return
		}
		if top.ug.Timestamp <= ug.Timestamp {
			break
		}
		newer, err := stack.pop()
		if err != nil {
			log.WithFields(log.Fields{
				"error":           err.Error(),
				"update_group_id": ug.ID,
			}).Error("error popping history stack when adding update group")
			return
		}
		newerUgs = append([]updateGroup{newer}, newerUgs...)
	}

	ugsToAdd := append([]updateGroup{ug}, newerUgs...)
	for _, update := range ugsToAdd {
		stack.insertUpdateGroupIntoStack(update)
	}
}

func (b *bounce) writeGroupConsensus(groupID uuid.UUID) error {
	b.consensusStore.Lock()
	defer b.consensusStore.Unlock()

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

	stack := b.consensusStore.groups[groupID]
	ugs := []updateGroup{}
	for _, gs := range stack.history {
		ugs = append(ugs, gs.ug)
	}

	return b.setRollbacksApplicationsAndGroupState(g, stack, ugs)
}
