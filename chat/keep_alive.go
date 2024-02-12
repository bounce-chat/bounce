package chat

//
// Keep alives are used to regularly send a small amount of data down all open connections to test that they are still alive
//
type keepAlive struct{}

func (ka keepAlive) getType() uint16 {
	return typeKeepAlive
}

func (ka keepAlive) getPayload() []byte {
	return []byte("keep-alive")
}

func (b *bounce) handleKeepAlive(peer string, payload []byte, catchUp bool) broadcastable {
	return nil
}
