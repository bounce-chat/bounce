package chat

import (
	"net"

	log "github.com/sirupsen/logrus"
)

func (bounce *Bounce) getHandlers() map[uint16]func(BounceAddress, []byte) {
	return map[uint16]func(BounceAddress, []byte){
		TYPE_DIRECT_MESSAGE: bounce.handleDirectMessage,
	}
}

func (bounce *Bounce) handleIncomingConnection(conn net.Conn) {
	handlers := bounce.getHandlers()
	peer := BounceAddress(conn.RemoteAddr().String())
	// Get the peer address
	// reject it if it isn't a known device?  Maybe don't want to if introductions / group membership is out of order
	// if it is known, add this connection to the larger device structure, which will mark the owner's user as online
	for {
		frameType, data, err := readFrame(conn)
		if err != nil {
			// tell the larger device pool this connection is dead
			return
		}
		// TODO: some type of filtering on which types of peers can send which types of messages
		handler, ok := handlers[frameType]
		if !ok {
			log.WithFields(log.Fields{
				"peer": peer,
				"type": frameType,
			}).Error("peer sent an unsupported frame type, disconnecting")
			// tell the larger device pool this connection is dead
			// TODO: conn.Close()?
			return
		} else {
			handler(peer, data)
		}
	}
}

func (bounce *Bounce) handleDirectMessage(peer BounceAddress, payload []byte) {
	// unmarshal the bytes
	// ensure that the source of the message lines up with the peer address
	// put it in the database (also saving that it was delivered to this peer)
	// send it to the UI
	//bounce.ui.ReceivedGroupMessage(GroupMessage{})
	// gossip it as needed
}
