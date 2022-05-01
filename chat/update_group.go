package chat

import (
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

type updateGroup struct {
	_msgpack  struct{}  `msgpack:",omitempty"`
	ID        uuid.UUID `gorm:"type:uuid;primary_key;"`
	Actor     uuid.UUID
	Target    uuid.UUID
	Timestamp int64
	Name      *string
	Retention *int64
	//AddUsers            []user
	RemoveUsers         *string
	PromoteUsersToAdmin *string
	RemoveAdmin         *string
	payload             []byte
	payloadMutex        sync.Mutex
}

func (ug *updateGroup) getID() uuid.UUID {
	return ug.ID
}

func (ug *updateGroup) getScope(myID uuid.UUID) int {
	return scopeGroup
}

func (ug *updateGroup) getDestination(myID uuid.UUID) uuid.UUID {
	return ug.Target
}

func (ug *updateGroup) getType() uint16 {
	return typeUpdateGroup
}

func (ug *updateGroup) getPayload() []byte {
	ug.payloadMutex.Lock()
	defer ug.payloadMutex.Unlock()

	if len(ug.payload) == 0 {
		bytes, err := msgpack.Marshal(ug)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal update group")
		}
		ug.payload = bytes
	}
	return ug.payload
}

func (ug *updateGroup) getTimestamp() int64 {
	return ug.Timestamp
}

func (b *bounce) handleUpdateGroup(peer string, payload []byte) {
	var update updateGroup
	err := msgpack.Unmarshal(payload, &update)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling group update")
		return
	}

	// If we already have this update, we just mark that this peer has it too and return

	// Otherwise, we save it and apply the changes
	//err = b.database.Create(&update).Error

	// apply it, inform the UI
	//b.userInterface.RenameGroup(update.Target, update.Actor, update.Name)
}

func (b *bounce) addUserToGroup(groupID, userID uuid.UUID) {
	log.WithFields(log.Fields{
		"group": groupID,
		"user":  userID,
	}).Info("UI wants to add user to group")
}

func (b *bounce) renameGroup(groupID uuid.UUID, newName string) {
	// TODO: make sure the new name is valid (length, character set, etc)

	update := updateGroup{
		Actor:     b.currentUserID(),
		Target:    groupID,
		Timestamp: time.Now().Unix(),
		Name:      &newName,
	}

	updateBytes, err := msgpack.Marshal(update)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group update")
	}

	b.handleUpdateGroup(b.network.Address(), updateBytes)
	go b.broadcast(&update)
}

func (b *bounce) changeGroupNotificationSettings(group uuid.UUID, enabled bool) {
	log.WithFields(log.Fields{
		"thread":                group,
		"notifications_enabled": enabled,
	}).Info("UI wants to change notification settings")
}
