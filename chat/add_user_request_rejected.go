package chat

import (
	"github.com/google/uuid"
)

type addUserRequestRejected struct{}

func (aurr *addUserRequestRejected) getID() uuid.UUID {
	return uuid.Nil
}

func (aurr *addUserRequestRejected) getScope(_ uuid.UUID) int {
	return scopeDevice
}

func (aurr *addUserRequestRejected) getDestination(_ uuid.UUID) uuid.UUID {
	return uuid.Nil
}

func (aurr *addUserRequestRejected) getType() uint16 {
	return typeAddUserRequestRejected
}

func (aurr *addUserRequestRejected) getPayload() []byte {
	return []byte{}
}

func (b *bounce) handleAddUserRequestRejected(peer string, payload []byte) {
	b.userInterface.AddUserRequestRejected()
}
