package chat

import (
	"errors"
	"net"
	"strings"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
)

func (bounce *Bounce) getHandlers() map[uint16]func(string, []byte) {
	return map[uint16]func(string, []byte){
		TYPE_DIRECT_MESSAGE:    bounce.handleDirectMessage,
		TYPE_REFERENCE_OFFER:   bounce.handleReferenceOffer,
		TYPE_REFERENCE_REQUEST: bounce.handleReferenceRequest,
		TYPE_CATCH_UP:          bounce.handleCatchUp,
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
	// Unmarshal the payload
	var dm DirectMessage
	err := msgpack.Unmarshal(payload, &dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling direct message")
		return
	}

	// Look up the device that sent it
	var srcDevice device
	err = bounce.database.Where("address = ?", peer).First(&srcDevice).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"peer": peer,
			}).Warn("an unknown device sent a direct message, ignoring")
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error looking up device while handling incoming direct message")
		}
	}

	// TODO: Ensure that this device is a sync device, or that it is either the source or destination of the message while the other side is us

	// If we have already seen this message, all we need to do is mark that this peer has the message as well
	var existingDM DirectMessage
	err = bounce.database.Where("id = ?", dm.ID).First(&existingDM).Error // TODO: other conditions?
	if err == nil {
		bounce.markDeliveredTo(&existingDM, peer)
		return
	}

	// Save the new message
	dm.DeliveredTo = peer
	err = bounce.database.Create(&dm).Error
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
	if len(ro.DirectMessages) > 0 {
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
				// TODO: want to make sure this can't be used to probe a devices for messages that it might already have from another chat,
				// to test group membership if a chat message from another group is known
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
	err = bounce.database.Where("id = ? AND for = ?", rr.ID, peer).First(&originalOffer).Error
	if err != nil {
		log.WithFields(log.Fields{
			"peer":     peer,
			"offer_id": rr.ID,
		}).Error("peer sent a reference request for an offer not in the database")
		return
	}
	bounce.devicePool.receivedAcksMutex.Lock()
	bounce.devicePool.receivedAcks[rr.ID.String()] = true
	bounce.devicePool.receivedAcksMutex.Unlock()

	var dev device
	res := bounce.database.Model(&device{}).Where("address = ?", peer).First(&dev)
	if res.Error != nil {
		log.WithFields(log.Fields{
			"error": res.Error.Error(),
		}).Error("error loading device that sent reference request")
		return
	}

	dmRequested := make(map[uuid.UUID]bool)
	if len(rr.DirectMessages) > 0 {
		for _, dmIDString := range strings.Split(rr.DirectMessages, ",") {
			dmID, err := uuid.Parse(dmIDString)
			if err != nil {
				log.WithFields(log.Fields{
					"error":  err.Error(),
					"string": dmIDString,
				}).Error("invalid UUID in reference request")
				continue
			}
			dmRequested[dmID] = true
		}
	}

	catchUpResponse := &catchUp{
		ID:          uuid.New(),
		destination: dev.ID,
	}

	if len(originalOffer.DirectMessages) > 0 {
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
			err = bounce.database.Where("id = ? AND (source = ? OR destination = ?)", dmID, dev.UserID, dev.UserID).First(&dm).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("reference request asks for unknown DM")
				// TODO: detect if any are legit but out of scope for security reasons?
				continue
			}

			if _, present := dmRequested[dmID]; present {
				catchUpResponse.DirectMessages = append(catchUpResponse.DirectMessages, dm.getPayload())
			} else {
				bounce.markDeliveredTo(&dm, peer)
			}
		}
	}

	if catchUpResponse.hasContent() {
		bounce.broadcastCatchUp(catchUpResponse)
	}

	if len(rr.DirectMessages) > len(originalOffer.DirectMessages) {
		log.WithFields(log.Fields{
			"offer_length":   len(originalOffer.DirectMessages),
			"request_length": len(rr.DirectMessages),
		}).Error("reference request is requesting more direct messages than were offered")
		// TODO: save the evidence for analysis?
	}

	bounce.database.Delete(&originalOffer)
}

func (bounce *Bounce) handleAck(peer string, payload []byte) {
	// for each thing being acked, update in the database that it was delivered to this peer
}

func (bounce *Bounce) handleCatchUp(peer string, payload []byte) {
	var cu catchUp
	err := msgpack.Unmarshal(payload, &cu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling catch up")
		return
	}

	// ack it

	for _, dmPayload := range cu.DirectMessages {
		bounce.handleDirectMessage(peer, dmPayload)
	}
}
