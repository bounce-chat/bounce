package chat

import (
	"net"
	"strings"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

func (bounce *Bounce) getHandlers() map[uint16]func(string, []byte) {
	return map[uint16]func(string, []byte){
		TYPE_DIRECT_MESSAGE:    bounce.handleDirectMessage,
		TYPE_REFERENCE_OFFER:   bounce.handleReferenceOffer,
		TYPE_REFERENCE_REQUEST: bounce.handleReferenceRequest,
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

	var srcDevice device
	res := bounce.database.Model(&device{}).Where("address = ?", peer).First(&srcDevice)
	if res.Error != nil {
		log.WithFields(log.Fields{
			"error": res.Error.Error(),
			"peer":  peer,
		}).Info("error loading device for reference offer handling")
	}

	response := &referenceRequest{
		ID:          ro.ID,
		destination: srcDevice.ID,
	}

	requestedDMs := []string{}
	for _, dmIDString := range strings.Split(ro.DirectMessages, ",") {
		dmID, err := uuid.Parse(dmIDString)
		if err != nil {
			log.WithFields(log.Fields{
				"error":  err.Error(),
				"string": dmIDString,
			}).Error("invalid UUID in reference offer")
			continue
		}

		var count int64
		err = bounce.database.Model(&DirectMessage{}).Where("id = ?", dmID).Count(&count).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error getting message from database")
			continue
		}
		if count == 0 {
			requestedDMs = append(requestedDMs, dmID.String())
		} else {
			// We already have this message, but if we didn't already know they had it, we update our records
			var dm DirectMessage
			err = bounce.database.First(&dm, dmID).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error looking up known DM in reference offer")
				continue
			}
			if !dm.isAlreadyDeliveredTo(peer) {
				bounce.markDeliveredTo(&dm, peer)
			}
		}
	}

	for i, dm := range requestedDMs {
		if i != 0 {
			response.DirectMessages = response.DirectMessages + ","
		}
		response.DirectMessages = response.DirectMessages + dm
	}

	bounce.broadcast(response)
}

func (bounce *Bounce) handleReferenceRequest(peer string, payload []byte) {
	var rr referenceRequest
	err := msgpack.Unmarshal(payload, &rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference request")
		return
	}

	var originalOffer referenceOffer
	err = bounce.database.First(&originalOffer, rr.ID).Error // TODO: store which device we gave this offer to as well to make sure they are the ones asking
	if err != nil {
		log.WithFields(log.Fields{
			"peer":     peer,
			"offer_id": rr.ID,
		}).Error("peer sent a reference request for an offer not in the database")
		return
	}

	var dev device
	res := bounce.database.Model(&device{}).Where("address = ?", peer).First(&dev)
	if res.Error != nil {
		log.WithFields(log.Fields{
			"error": res.Error.Error(),
		}).Error("error loading device that sent reference request")
		return
	}

	rd := bounce.getRemoteDevice(peer)

	dmRequested := make(map[uuid.UUID]bool)
	for _, dmIDString := range strings.Split(rr.DirectMessages, ",") {
		dmID, err := uuid.Parse(dmIDString)
		if err != nil {
			log.WithFields(log.Fields{
				"error":  err.Error(),
				"string": dmIDString,
			}).Error("invalid UUID in reference offer generated locally")
			continue
		}
		dmRequested[dmID] = true
	}

	for _, dmIDString := range strings.Split(originalOffer.DirectMessages, ",") {
		dmID, err := uuid.Parse(dmIDString)
		if err != nil {
			log.WithFields(log.Fields{
				"error":  err.Error(),
				"string": dmIDString,
			}).Error("invalid UUID in reference offer generated locally")
			continue
		}

		var dm DirectMessage
		err = bounce.database.
			//Where("destination = ?", dev.UserID).
			//Or("source = ?", dev.UserID). // TODO: validate after the fact?  Database is preferred
			Find(&dm, dmID).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("reference request asks for unknown DM")
			continue
		}

		if _, present := dmRequested[dmID]; present {
			//go func(dmCopy DirectMessage) {
			rd.messages <- &dm //Copy
			//}(dm) // TODO: needed?  better way to copy?  would prefer to do this sync not in routines
		} else {
			bounce.markDeliveredTo(&dm, peer)
		}
	}

	bounce.database.Delete(&originalOffer)
}

func (bounce *Bounce) handleAck(peer string, payload []byte) {
	// for each thing being acked, update in the database that it was delivered to this peer
}
