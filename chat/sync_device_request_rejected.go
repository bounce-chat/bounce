package chat

type syncDeviceRequestRejected struct{}

func (sdrr *syncDeviceRequestRejected) getType() uint16 {
	return typeSyncDeviceRequestRejected
}

func (sdrr *syncDeviceRequestRejected) getPayload() []byte {
	return []byte{}
}

func (b *bounce) handleSyncDeviceRequestRejected(peer string, payload []byte) {
	b.userInterface.SyncDeviceRequestRejected()
}
