package chat

import (
	"fmt"
	"net"

	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

func (bounce *Bounce) getHandlers() map[uint16]func(string, []byte) {
	return map[uint16]func(string, []byte){
		TYPE_DIRECT_MESSAGE:  bounce.handleDirectMessage,
		TYPE_REFERENCE_OFFER: bounce.handleReferenceOffer,
	}
}

func (bounce *Bounce) readFrames(conn net.Conn) { // TODO: move to device pool?
	handlers := bounce.getHandlers()
	peer := conn.RemoteAddr().String()
	// Get the peer address
	// reject it if it isn't a known device?  Maybe don't want to if introductions / group membership is out of order
	// If it isn't know perhaps we put it in some limited handshake flow for new devices
	for {
		frameType, data, err := readFrame(conn) // TODO: just read the header first, make sure we want to read the rest in the context of the device (untrusted devices can't send large messages, etc)
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
			conn.Close()
			return
		} else {
			go handler(peer, data)
		}
	}
}

func (bounce *Bounce) handleDirectMessage(peer string, payload []byte) {
	var dm DirectMessage
	err := msgpack.Unmarshal(payload, &dm)
	if err != nil {
		log.Error("error unmarshalling direct message")
		return
	}

	// check if we already got this message, if so all we need to do is mark that it has been delivered to this device already
	// ensure that the source of the message lines up with the peer address
	// put it in the database (also saving that it was delivered to this peer and the sender)
	err = bounce.database.Create(&dm).Error // TODO: make sure to include that is was delivered to the device that sent it before saving
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error saving incoming direct message")
	}
	// send an ack to the sender that we got it
	// gossip it, or references to it, as needed
	bounce.userInterface.ReceivedDirectMessage(dm)
}

func (bounce *Bounce) handleReferenceOffer(peer string, payload []byte) {
	var ro referenceOffer
	err := msgpack.Unmarshal(payload, &ro)
	if err != nil {
		log.Error("error unmarshalling reference offer")
		return
	}
	fmt.Printf("%+v\n", ro)
}
