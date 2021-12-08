package chat

import (
	"github.com/google/uuid"
)

type syncDeviceRequestRejected struct{}

func (sdrr *syncDeviceRequestRejected) getScope(_ uuid.UUID) int {
	return scopeDevice
}

func (sdrr *syncDeviceRequestRejected) getDestination(_ uuid.UUID) uuid.UUID {
	return uuid.Nil
}

func (sdrr *syncDeviceRequestRejected) getType() uint16 {
	return typeSyncDeviceRequestRejected
}

func (sdrr *syncDeviceRequestRejected) getPayload() []byte {
	return []byte{}
}

func (sdrr *syncDeviceRequestRejected) isAlreadyDeliveredTo(address string) bool {
	return false
}

func (b *bounce) handleSyncDeviceRequestRejected(peer string, payload []byte) {
	b.userInterface.SyncDeviceRequestRejected()
}
