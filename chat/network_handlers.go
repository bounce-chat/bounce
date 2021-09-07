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
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling direct message")
		return
	}

	// ensure that the source of the message lines up with the peer address
	// check if we already got this message, if so all we need to do is mark that it has been delivered to this device already

	dm.DeliveredTo = peer
	err = bounce.database.Create(&dm).Error // TODO: make sure to include that is was delivered to the device that sent it before saving
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error saving incoming direct message")
	}

	// send an ack to the sender that we got it
	// gossip it as needed, or references to it (if it's a small group and we're pretty sure that this peer is connected to everyone else, decide that here or automatically in the broadcast function?)

	bounce.userInterface.ReceivedDirectMessage(dm)
}

func (bounce *Bounce) handleReferenceOffer(peer string, payload []byte) {
	var ro referenceOffer
	err := msgpack.Unmarshal(payload, &ro)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference offer")
		return
	}
	// Prep a reference request in response to this offer.
	// for each thing in the offer, if we already have it, make sure to mark that this device has it as well
	// if we don't have it, put in the reference request
	// broadcast the reference request back to this device
	fmt.Printf("%+v\n", ro)
}

func (bounce *Bounce) handleReferenceRequest(peer string, payload []byte) {
	// look up the offer by ID
	// anything that isn't in this request, but that was in the offer, we can assume the device already has
	// for the rest of the messages, look them up, and broadcast them at this specific device
}

func (bounce *Bounce) handleAck(peer string, payload []byte) {
	// for each thing being acked, update in the database that it was delivered to this peer
}
