package chat

import (
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

type catchUp struct {
	_msgpack       struct{} `msgpack:",omitempty"`
	ID             uuid.UUID
	DirectMessages [][]byte
	destination    uuid.UUID
	payload        []byte
}

func (cu *catchUp) getScope() int {
	return DEVICE_SCOPE
}

func (cu *catchUp) getDestination() uuid.UUID {
	return cu.destination

}

func (cu *catchUp) getType() uint16 {
	return TYPE_CATCH_UP
}

func (cu *catchUp) getPayload() []byte {
	if len(cu.payload) == 0 {
		bytes, err := msgpack.Marshal(cu)
		if err != nil {
			// TODO: how to handle?
		}
		cu.payload = bytes
	}
	return cu.payload
}

func (cu *catchUp) isAlreadyDeliveredTo(address string) bool {
	return false
}

func (cu *catchUp) hasContent() bool {
	if len(cu.DirectMessages) > 0 {
		return true
	}
	return false
}

// TODO: rename or something after these "wait for response" flows are rethought
func (bounce *Bounce) broadcastCatchUp(cu *catchUp) {
	giveUpTime := time.Now().Add(5 * time.Minute)
	for {
		bounce.broadcast(cu)
		time.Sleep(30 * time.Second) // TODO: derive from message size?
		bounce.devicePool.receivedAcksMutex.Lock()
		_, ok := bounce.devicePool.receivedAcks[cu.ID.String()]
		bounce.devicePool.receivedAcksMutex.Unlock()
		if ok {
			// we got the request, our offer was delivered
			bounce.devicePool.receivedAcksMutex.Lock()
			delete(bounce.devicePool.receivedAcks, cu.ID.String())
			bounce.devicePool.receivedAcksMutex.Unlock()
			return
		}
		if time.Now().After(giveUpTime) {
			log.WithFields(log.Fields{
				"id":          cu.ID,
				"destination": cu.getDestination(),
			}).Warn("gave up attempting to deliver catch up")
			return
		}
	}
}

func (bounce *Bounce) handleCatchUp(peer string, payload []byte) {
	var cu catchUp
	err := msgpack.Unmarshal(payload, &cu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling catch up")
		return
	}

	var dev device
	res := bounce.database.Model(&device{}).Where("address = ?", peer).First(&dev)
	if res.Error != nil {
		log.WithFields(log.Fields{
			"error": res.Error.Error(),
		}).Error("error loading device that sent catch up")
		return
	}
	go bounce.broadcast(&ack{
		destination: dev.ID,
		CatchUps:    cu.ID.String(),
	})

	for _, dmPayload := range cu.DirectMessages {
		bounce.handleDirectMessage(peer, dmPayload)
	}
}
