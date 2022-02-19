package chat

import (
	"sort"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

var catchUpMutex sync.Mutex

type frame struct {
	Type    uint16
	Payload []byte
}

type catchUp struct {
	_msgpack       struct{} `msgpack:",omitempty"`
	ID             uuid.UUID
	Frames         []frame
	broadcastables sortableBroadcastables
	destination    uuid.UUID
	payload        []byte
	payloadMutex   sync.Mutex
}

func (cu *catchUp) getID() uuid.UUID {
	return cu.ID
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
		sort.Sort(cu.broadcastables)
		for _, br := range cu.broadcastables {
			cu.Frames = append(cu.Frames, frame{Type: br.getType(), Payload: br.getPayload()})
		}

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

func (cu *catchUp) hasContent() bool {
	return len(cu.broadcastables) > 0
}

func (b *bounce) handleCatchUp(peer string, payload []byte) {
	catchUpMutex.Lock()
	defer catchUpMutex.Unlock()

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

	handlers := b.getHandlers()
	for _, fr := range cu.Frames {
		handler, ok := handlers[fr.Type]
		if !ok {
			log.WithFields(log.Fields{
				"peer": peer,
				"type": fr.Type,
			}).Warn("peer sent a catch up frame type that doesn't have a handler")
			continue
		}
		handler(peer, fr.Payload)
	}
}
