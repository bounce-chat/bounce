package chat

import (
	"errors"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

var errStackEmpty = errors.New("stack is empty")

type canonicalStack struct {
	myID         uuid.UUID
	history      []groupState
	historyStash []groupState
}

func newCanonicalStack(initialState groupState, myID uuid.UUID) *canonicalStack {
	return &canonicalStack{
		myID:    myID,
		history: []groupState{initialState},
	}
}

func (cs *canonicalStack) push(ug updateGroup) error {
	top, err := cs.top()
	if err != nil {
		return err
	}

	updatedGroupState, err := applyUpdateGroupToState(top, ug)
	if err != nil {
		log.WithFields(log.Fields{
			"update_group_id": ug.ID,
			"type":            ug.Type,
			"error":           err.Error(),
		}).Error("cannot push update group onto history stack")
		return err
	}

	cs.history = append(cs.history, updatedGroupState)

	return nil
}

func (cs *canonicalStack) pop() (updateGroup, error) {
	if cs.empty() {
		return updateGroup{}, errStackEmpty
	}

	lastItem := cs.history[len(cs.history)-1]
	cs.history = cs.history[:len(cs.history)-1]
	return lastItem.ug, nil
}

func (cs *canonicalStack) top() (groupState, error) {
	if cs.empty() {
		return groupState{}, errStackEmpty
	}
	return cs.history[len(cs.history)-1], nil
}

func (cs *canonicalStack) empty() bool {
	return len(cs.history) == 0
}

func (cs *canonicalStack) stash() {
	cs.historyStash = cs.history
}

func (cs *canonicalStack) restore() {
	cs.history = cs.historyStash
}

// Given an update group, add it into the history stack if it should be applied, detecting and removing any conflicts in the process
func (b *bounce) insertUpdateGroupIntoStack(cs *canonicalStack, ug updateGroup) {
	// Don't allow updated that were signed by a device after it was revoked
	var dev device
	err := b.database.First(&dev, "address = ?", ug.Signer).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"update_group_id": ug.ID,
				"address":         ug.Signer,
			}).Error("cannot find signing device for update group")
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up device")
		}
	}
	if dev.RevokedAt > 0 && dev.RevokedAt < ug.Timestamp {
		return
	}

	// Make sure the payload of this update is valid for its type
	if !ug.validPayloadFormat() {
		log.WithFields(log.Fields{
			"id": ug.ID,
		}).Error("ignoring update group with invalid data")
	}

	// Get the current state of history
	lastState, err := cs.top()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("cannot insert update group into history")
		return
	}

	// Enforce time ordering on everything after the group creation
	if len(cs.history) > 1 {
		if ug.Timestamp < lastState.ug.Timestamp {
			log.WithFields(log.Fields{
				"previous": lastState.ug.ID,
				"current":  ug.ID,
			}).Error("out of order update group inserted into canonical history")
			return
		}
	}

	// Stop allowing any new changes once a group has been blocked
	if lastState.isBlocked(cs.myID) {
		return
	}

	// Ignore this update if it changes nothing
	if changeIsNOP(lastState, ug) {
		return
	}

	// Check if this user has permission to perform this change
	if err = stateChangeAllowed(lastState, ug, cs.myID); err == nil {
		// If it is allowed, apply it
		cs.push(ug)
	} else {
		// If this change is not allowed, check if it is confirmed
		confirmed := (float64(ug.confirmingUsers()) / float64(len(lastState.users))) > 0.5

		if confirmed {
			// If this change is not allowed and is confirmed, pop through history until the conflicting change is identified
			recheck := []updateGroup{}
			cs.stash()
			for {
				// Remove the lastest change and add it to a slice
				removed, err := cs.pop()
				if err != nil {
					// This shouldn't be possible
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("error popping group state history stack")
				}
				recheck = append([]updateGroup{removed}, recheck...)

				// If the history is now empty, then this change was never allowed, so we ignore it even though it's confirmed and reset the stack
				if cs.empty() {
					cs.restore()
					return
				}

				// Now that one item has been removed, check if that makes this change allowed
				newTop, err := cs.top()
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("error getting top of group state history stack")

				}
				if err = stateChangeAllowed(newTop, ug, cs.myID); err == nil {
					// If this chnage is now allwed, then the conflict was the last thing we removed from the stack
					conflict := recheck[0]

					// Check if this conflict was confirmed
					conflictConfirmed := (float64(conflict.confirmingUsers()) / float64(len(newTop.users))) > 0.5
					if conflictConfirmed {
						// If the conflict was confirmed as well, then the conflict wins because it's older, and we ignore this change
						cs.restore()
						break
					} else {
						// If the conflict is not confirmed then we exclude it, and attempt to re-add everything that happened since the conflict was removed
						for _, rc := range recheck[1:] {
							b.insertUpdateGroupIntoStack(cs, rc)
						}
						break
					}
				}
			}
		} else {
			// If this change is not allowed and not confirmed, do not add it to history
			return
		}
	}
}
