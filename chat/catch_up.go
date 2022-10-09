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
		// A stable sort is used because the catch up is originally packed with users
		// before devices.  Users and devices both always have a timestamp of 0, so
		// we want to make sure the users stay ahead of the devices.
		sort.Stable(cu.broadcastables)
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

	dev, deviceExists := b.getDeviceFromAddress(peer)
	if deviceExists {
		go b.broadcast(&ack{
			destination: dev.ID,
			CatchUps:    cu.ID.String(),
		})
	}

	handlers := b.getHandlers()
	for _, fr := range cu.Frames {
		if !deviceExists && !frameAllowedFromUnknownPeer(fr.Type) {
			log.WithFields(log.Fields{
				"frame_type": fr.Type,
			}).Warn("an unknown device sent a catch up that included frames that are not allowed from unknown devices")
			return
		}

		if fr.Type == typeCatchUp {
			log.Warn("refusing to processes recursive catch up")
			return
		}

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

	if !deviceExists {
		if dev, deviceNowExists := b.getDeviceFromAddress(peer); deviceNowExists {
			// An unknown device sent a catch up that included frames that prove we should add the device,
			// since we didn't initially offer references to this device we should now do so now that we
			// have context on what this device is
			go b.broadcast(&ack{
				destination: dev.ID,
				CatchUps:    cu.ID.String(),
			})
			// TODO: initialte reference flow
		}
	}
}
