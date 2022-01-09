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
	_msgpack              struct{} `msgpack:",omitempty"`
	ID                    uuid.UUID
	DirectMessages        string // Comma-separated list of DM UUIDs
	UpdateLocalDMSettings string
	CatchUps              string
	Devices               string
	Users                 string
	destination           uuid.UUID
	payload               []byte
	payloadMutex          sync.Mutex
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
	b.handleAckCatchUps(peer, a)
	b.handleAckUpdateLocalDMSettings(peer, a)
	b.handleAckDevices(peer, a)
	b.handleAckUsers(peer, a)
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

func (b *bounce) handleAckUpdateLocalDMSettings(peer string, a ack) {
	if len(a.UpdateLocalDMSettings) > 0 {
		for _, uldsIDString := range strings.Split(a.UpdateLocalDMSettings, ",") {
			uldsID, err := uuid.Parse(uldsIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": uldsIDString,
				}).Error("invalid ulds UUID in ack")
				continue
			}

			var ulds updateLocalDMSettings
			err = b.database.First(&ulds, "id = ?", uldsID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"id":   uldsID,
						"peer": peer,
					}).Warn("unknown update local DM settings acked")
				} else {
					log.WithFields(log.Fields{
						"id":    uldsID,
						"error": err.Error(),
					}).Fatal("database error querying for update local DM settings")
				}
			} else {
				b.markDeliveredTo(&ulds, peer)
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

func (b *bounce) handleAckUsers(peer string, a ack) {
	if len(a.Users) > 0 {
		for _, userIDString := range strings.Split(a.Users, ",") {
			userID, err := uuid.Parse(userIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": userIDString,
				}).Error("invalid user UUID in ack")
				continue
			}

			var u user
			err = b.database.First(&u, "id = ?", userID).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					log.WithFields(log.Fields{
						"id":   userID,
						"peer": peer,
					}).Warn("unknown user acked")
				} else {
					log.WithFields(log.Fields{
						"id":    userID,
						"error": err.Error(),
					}).Fatal("database error querying for user")
				}
			} else {
				b.markDeliveredTo(&u, peer)
			}
		}
	}
}
