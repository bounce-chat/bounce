package chat

import (
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

type catchUp struct {
	_msgpack              struct{} `msgpack:",omitempty"`
	ID                    uuid.UUID
	DirectMessages        [][]byte
	UpdateLocalDMSettings [][]byte
	Devices               [][]byte
	Users                 [][]byte
	destination           uuid.UUID
	payload               []byte
	payloadMutex          sync.Mutex
}

func (cu *catchUp) getScope(_ uuid.UUID) int {
	return scopeDevice
}

func (cu *catchUp) getDestination(_ uuid.UUID) uuid.UUID {
	return cu.destination

}

func (cu *catchUp) getType() uint16 {
	return typeCatchUp
}

func (cu *catchUp) getPayload() []byte {
	cu.payloadMutex.Lock()
	defer cu.payloadMutex.Unlock()

	if len(cu.payload) == 0 {
		bytes, err := msgpack.Marshal(cu)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal catch up")
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
	if len(cu.UpdateLocalDMSettings) > 0 {
		return true
	}
	if len(cu.Devices) > 0 {
		return true
	}
	if len(cu.Users) > 0 {
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
				"destination": cu.getDestination(b.currentUserID()),
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
		}).Warn("unable to find device that sent catch up, ignoring")
		return
	}
	go b.broadcast(&ack{
		destination: dev.ID,
		CatchUps:    cu.ID.String(),
	})

	for _, userPayload := range cu.Users {
		b.handleUser(peer, userPayload)
	}
	for _, devicePayload := range cu.Devices {
		b.handleDevice(peer, devicePayload)
	}
	for _, uldsPayload := range cu.UpdateLocalDMSettings {
		b.handleUpdateLocalDMSettings(peer, uldsPayload)
	}
	for _, dmPayload := range cu.DirectMessages {
		b.handleDirectMessage(peer, dmPayload)
	}
}
