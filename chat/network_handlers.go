package chat

import (
	"net"

	log "github.com/sirupsen/logrus"
)

func (bounce *Bounce) getHandlers() map[uint16]func([]byte) { // TODO: custom types for these uint16s
	return map[uint16]func([]byte){
		0: bounce.handleChatMessage,
	}
}

func (bounce *Bounce) handleIncomingConnection(conn net.Conn) {
	handlers := bounce.getHandlers()
	// Get the peer address
	// reject it if it isn't a known device?  Maybe don't want to if introductions / group membership is out of order
	// if it is known, add this connection to the larger device structure, which will mark the owner's user as online
	for {
		frameType, data, err := readFrame(conn)
		if err != nil {
			// tell the larger device pool this connection is dead
			return
		}
		handler, ok := handlers[frameType]
		if !ok {
			log.WithFields(log.Fields{
				"peer": conn.RemoteAddr(),
				"type": frameType,
			}).Error("peer sent an unsupported frame type, disconnecting")
			// tell the larger device pool this connection is dead
			// TODO: conn.Close()?
			return
		} else {
			handler(data)
		}
	}
}

func (bounce *Bounce) handleChatMessage(payload []byte) {
	// protobuf unmarshal the bytes
	// put it in the database
	// send it to the UI
	// gossip it as needed
}
