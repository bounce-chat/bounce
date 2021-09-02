package chat

import (
	"net"

	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

func (bounce *Bounce) getHandlers() map[uint16]func(string, []byte) {
	return map[uint16]func(string, []byte){
		TYPE_DIRECT_MESSAGE: bounce.handleDirectMessage,
	}
}

func (bounce *Bounce) readFrames(conn net.Conn) {
	handlers := bounce.getHandlers()
	peer := conn.RemoteAddr().String()
	// Get the peer address
	// reject it if it isn't a known device?  Maybe don't want to if introductions / group membership is out of order
	// If it isn't know perhaps we put it in some limited handshake flow for new devices
	for {
		frameType, data, err := readFrame(conn)
		if err != nil {
			// TODO: do I need to tell the larger device pool this connection is dead?
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

func (bounce *Bounce) handleDirectMessage(peer string, payload []byte) {
	var dm directMessage
	err := msgpack.Unmarshal(payload, &dm)
	if err != nil {
		log.Error("error unmarshalling direct message")
		return
	}
	bounce.userInterface.ReceivedDirectMessage(DirectMessage{
		//Destination: directMessage.Destination,
		Source: dm.Source,
		Text:   dm.Text,
	})
	// ensure that the source of the message lines up with the peer address
	// put it in the database (also saving that it was delivered to this peer and the sender)
	// gossip it as needed
}
