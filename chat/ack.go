package chat

import (
	"strings"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

type ack struct {
	_msgpack       struct{}  `msgpack:",omitempty"`
	ID             uuid.UUID `gorm:"type:uuid;primary_key;"` // TODO: we don't actually store these
	For            string    `msgpack:"-"`
	DirectMessages string    // Comma-separated list of DM UUIDs TODO: since this isn't going in the database can we use a proper slice here?
	CatchUps       string    // for ack-ing catch ups to ensure delivery.  TODO: maybe not what we stick with, maybe break acks out of references
	destination    uuid.UUID
	payload        []byte
}

func (a *ack) getScope() int {
	return scopeDevice
}

func (a *ack) getDestination() uuid.UUID {
	return a.destination
}

func (a *ack) getType() uint16 {
	return typeAck
}

func (a *ack) getPayload() []byte {
	if len(a.payload) == 0 {
		bytes, err := msgpack.Marshal(a)
		if err != nil {
			// TODO: how to handle?
		}
		a.payload = bytes
	}
	return a.payload
}

func (a *ack) isAlreadyDeliveredTo(address string) bool {
	return false
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
			err = b.database.First(&dm, dmID).Error
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

			b.devicePool.receivedAcksMutex.Lock()
			b.devicePool.receivedAcks[catchUpID.String()] = true
			b.devicePool.receivedAcksMutex.Unlock()
		}
	}
}
