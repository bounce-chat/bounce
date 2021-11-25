package chat

import (
	"github.com/google/uuid"
)

type keepAlive struct {
	destination uuid.UUID
}

func (ka keepAlive) getScope() int {
	return scopeDevice
}

func (ka keepAlive) getDestination() uuid.UUID {
	return ka.destination
}

func (ka keepAlive) getType() uint16 {
	return typeKeepAlive
}

func (ka keepAlive) getPayload() []byte {
	return []byte("keep-alive")
}

func (ka keepAlive) isAlreadyDeliveredTo(address string) bool {
	return false
}

func (b *bounce) handleKeepAlive(peer string, payload []byte) {
	return
}
