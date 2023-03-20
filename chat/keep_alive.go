package chat

type keepAlive struct{}

func (ka keepAlive) getType() uint16 {
	return typeKeepAlive
}

func (ka keepAlive) getPayload() []byte {
	return []byte("keep-alive")
}

func (b *bounce) handleKeepAlive(peer string, payload []byte) {
	return
}
