package chat

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type keepAlive struct {
	destination uuid.UUID
}

func (ka keepAlive) getScope() int {
	return DEVICE_SCOPE
}

func (ka keepAlive) getDestination() uuid.UUID {
	return ka.destination
}

func (ka keepAlive) getType() uint16 {
	return TYPE_KEEP_ALIVE
}

func (ka keepAlive) getPayload() []byte {
	return []byte("keep-alive")
}

func (ka keepAlive) isAlreadyDeliveredTo(address string) bool {
	return false
}

func (bounce *Bounce) handleKeepAlive(peer string, payload []byte) {
	log.Info("got a keep alive packet")
	return
}
