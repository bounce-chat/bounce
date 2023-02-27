package chat

import (
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DirectMessages are comma separated for consistency with reference offers, which must do this
// since SQLite doesn't support slices
type ack struct {
	_msgpack       struct{} `msgpack:",omitempty"`
	ID             uuid.UUID
	DirectMessages string // Comma-separated list of DM UUIDs
	GroupMessages  string
	CatchUps       string
	Devices        string
	AddUsers       string
	GroupCreations string
	UpdateGroups   string
	UpdateDMs      string
	destination    uuid.UUID
	payload        []byte
	payloadMutex   sync.Mutex
}

func (a *ack) getID() uuid.UUID {
	return a.ID
}

func (a *ack) getScope(_ uuid.UUID) int {
	return scopeDevice
}

func (a *ack) getDestination(_ uuid.UUID) uuid.UUID {
	return a.destination
}

func (a *ack) getType() uint16 {
	return typeAck
}

func (a *ack) getPayload() []byte {
	a.payloadMutex.Lock()
	defer a.payloadMutex.Unlock()

	if len(a.payload) == 0 {
		bytes, err := msgpack.Marshal(a)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal ack")
		}
		a.payload = bytes
	}
	return a.payload
}

func (b *bounce) handleAck(peer string, payload []byte) {
	var a ack
	err := msgpack.Unmarshal(payload, &a)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling ack")
		return
	}

	b.handleAckDirectMessages(peer, a)
	b.handleAckGroupMessages(peer, a)
	b.handleAckCatchUps(peer, a)
	b.handleAckUpdateDMs(peer, a)
	b.handleAckDevices(peer, a)
	b.handleAckAddUsers(peer, a)
	b.handleAckGroupCreations(peer, a)
	b.handleAckUpdateGroups(peer, a)
}

func (b *bounce) sendDirectAck(peer string, a *ack) {
	rd := b.getRemoteDevice(peer)
	rd.messages <- a
}

func (b *bounce) handleAckDirectMessages(peer string, a ack) {
	if len(a.DirectMessages) > 0 {
		for _, dmIDString := range strings.Split(a.DirectMessages, ",") {
			dmID, err := uuid.Parse(dmIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": dmIDString,
				}).Error("invalid DM UUID in ack")
				continue
			}

			var dm DirectMessage
			err = b.database.First(&dm, "id = ?", dmID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"peer":  peer,
				}).Error("ack of unknown DM from peer")
				// TODO: could be abuse attempted to waste time hitting the database, perhaps should bail / reset connection
				continue
			}
			// TODO: confirm the device should be able to see this DM
			b.markDeliveredTo(&dm, peer)

			// Now that we know the message has been delivered, if the message expires we start the clock on retention
			// by setting the absolute time the message should be delete at as now + the retention time
			if dm.RetentionSeconds != 0 && dm.DeleteAt == 0 {
				deleteAt := time.Now().Unix() + dm.RetentionSeconds
				err := b.database.Model(&dm).Update("delete_at", deleteAt).Error
				if err != nil {
					log.WithFields(log.Fields{
						"message_id": dm.ID,
						"error":      err.Error(),
					}).Fatal("error updating delete_at of acked direct message")
				}
				b.userInterface.UpdateMessageDeletionTime(dm.ID, deleteAt)
			}
		}
	}
}

func (b *bounce) handleAckGroupMessages(peer string, a ack) {
	if len(a.GroupMessages) > 0 {
		for _, gmIDString := range strings.Split(a.GroupMessages, ",") {
			gmID, err := uuid.Parse(gmIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": gmIDString,
				}).Error("invalid GM UUID in ack")
				continue
			}

			var gm GroupMessage
			err = b.database.First(&gm, "id = ?", gmID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"peer":  peer,
				}).Error("ack of unknown GM from peer")
				// TODO: could be abuse attempted to waste time hitting the database, perhaps should bail / reset connection
				continue
			}
			// TODO: confirm the device should be able to see this DM
			b.markDeliveredTo(&gm, peer)

			// Now that we know the message has been delivered, if the message expires we start the clock on retention
			// by setting the absolute time the message should be delete at as now + the retention time
			if gm.RetentionSeconds != 0 && gm.DeleteAt == 0 {
				deleteAt := time.Now().Unix() + gm.RetentionSeconds
				err := b.database.Model(&gm).Update("delete_at", deleteAt).Error
				if err != nil {
					log.WithFields(log.Fields{
						"message_id": gm.ID,
						"error":      err.Error(),
					}).Fatal("error updating delete_at of acked group message")
				}
				b.userInterface.UpdateMessageDeletionTime(gm.ID, deleteAt)
			}
		}
	}
}

func (b *bounce) handleAckCatchUps(peer string, a ack) {
	if len(a.CatchUps) > 0 {
		for _, catchUpIDString := range strings.Split(a.CatchUps, ",") {
			catchUpID, err := uuid.Parse(catchUpIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": catchUpIDString,
				}).Error("invalid catch up UUID in ack")
				continue
			}

			// Mark this catch up as delivered so we can stop broadcasting it
			err = b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&deliveryRecord{
				Destination: peer,
				FrameID:     catchUpID,
				FrameType:   typeCatchUp,
			}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error creating delivery record for catch up")
			}
		}
	}
}

func (b *bounce) handleAckUpdateDMs(peer string, a ack) {
	if len(a.UpdateDMs) > 0 {
		for _, udIDString := range strings.Split(a.UpdateDMs, ",") {
			udID, err := uuid.Parse(udIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": udIDString,
				}).Error("invalid ud UUID in ack")
				continue
			}

			var ud updateDM
			err = b.database.First(&ud, "id = ?", udID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"id":   udID,
						"peer": peer,
					}).Warn("unknown update DM settings acked")
				} else {
					log.WithFields(log.Fields{
						"id":    udID,
						"error": err.Error(),
					}).Fatal("database error querying for update DM settings")
				}
			} else {
				b.markDeliveredTo(&ud, peer)
			}
		}
	}
}

func (b *bounce) handleAckDevices(peer string, a ack) {
	if len(a.Devices) > 0 {
		for _, deviceIDString := range strings.Split(a.Devices, ",") {
			deviceID, err := uuid.Parse(deviceIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": deviceIDString,
				}).Error("invalid device UUID in ack")
				continue
			}

			var dev device
			err = b.database.Preload(clause.Associations).First(&dev, "id = ?", deviceID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"id":   deviceID,
						"peer": peer,
					}).Warn("unknown device acked")
				} else {
					log.WithFields(log.Fields{
						"id":    deviceID,
						"error": err.Error(),
					}).Fatal("database error querying for device")
				}
			} else {
				b.markDeliveredTo(&dev, peer)
			}
		}
	}
}

func (b *bounce) handleAckAddUsers(peer string, a ack) {
	if len(a.AddUsers) > 0 {
		for _, addUserIDString := range strings.Split(a.AddUsers, ",") {
			addUserID, err := uuid.Parse(addUserIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": addUserIDString,
				}).Error("invalid add user UUID in ack")
				continue
			}

			var au addUser
			err = b.database.First(&au, "id = ?", addUserID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"id":   addUserID,
						"peer": peer,
					}).Warn("unknown add user acked")
				} else {
					log.WithFields(log.Fields{
						"id":    addUserID,
						"error": err.Error(),
					}).Fatal("database error querying for add user")
				}
			} else {
				b.markDeliveredTo(&au, peer)
			}
		}
	}
}

func (b *bounce) handleAckGroupCreations(peer string, a ack) {
	if len(a.GroupCreations) > 0 {
		for _, groupCreationIDString := range strings.Split(a.GroupCreations, ",") {
			groupCreationID, err := uuid.Parse(groupCreationIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": groupCreationIDString,
				}).Error("invalid group creation UUID in ack")
				continue
			}

			var gc groupCreation
			err = b.database.First(&gc, "id = ?", groupCreationID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"id":   groupCreationID,
						"peer": peer,
					}).Warn("unknown group creation acked")
				} else {
					log.WithFields(log.Fields{
						"id":    groupCreationID,
						"error": err.Error(),
					}).Fatal("database error querying for group creation")
				}
			} else {
				b.markDeliveredTo(&gc, peer)
			}
		}
	}
}

func (b *bounce) handleAckUpdateGroups(peer string, a ack) {
	if len(a.UpdateGroups) > 0 {
		for _, updateGroupIDString := range strings.Split(a.UpdateGroups, ",") {
			updateGroupID, err := uuid.Parse(updateGroupIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": updateGroupIDString,
				}).Error("invalid update group UUID in ack")
				continue
			}

			var ug updateGroup
			err = b.database.First(&ug, "id = ?", updateGroupID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"id":   updateGroupID,
						"peer": peer,
					}).Warn("unknown update group acked")
				} else {
					log.WithFields(log.Fields{
						"id":    updateGroupID,
						"error": err.Error(),
					}).Fatal("database error querying for update group")
				}
			} else {
				b.markDeliveredTo(&ug, peer)
			}
		}
	}
}
