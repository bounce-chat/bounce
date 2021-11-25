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
	return scopeDevice
}

func (cu *catchUp) getDestination() uuid.UUID {
	return cu.destination

}

func (cu *catchUp) getType() uint16 {
	return typeCatchUp
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

func (b *bounce) broadcastCatchUp(cu *catchUp) {
	giveUpTime := time.Now().Add(5 * time.Minute)
	for {
		b.broadcast(cu)
		time.Sleep(30 * time.Second) // TODO: derive from message size?
		b.devicePool.receivedAcksMutex.Lock()
		_, ok := b.devicePool.receivedAcks[cu.ID.String()]
		b.devicePool.receivedAcksMutex.Unlock()
		if ok {
			// we got the request, our offer was delivered
			b.devicePool.receivedAcksMutex.Lock()
			delete(b.devicePool.receivedAcks, cu.ID.String())
			b.devicePool.receivedAcksMutex.Unlock()
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

func (b *bounce) handleCatchUp(peer string, payload []byte) {
	var cu catchUp
	err := msgpack.Unmarshal(payload, &cu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling catch up")
		return
	}

	dev, exists := b.getDeviceFromAddress(peer)
	if !exists {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Error("error loading device that sent catch up")
		return
	}
	go b.broadcast(&ack{
		destination: dev.ID,
		CatchUps:    cu.ID.String(),
	})

	for _, dmPayload := range cu.DirectMessages {
		b.handleDirectMessage(peer, dmPayload)
	}
}
