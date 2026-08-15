package chat

import (
	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

// A reference request is sent in response to a reference offer if the offer contained frame references that
// are needed by the device
type referenceRequest struct {
	References []frameReference
}

func (rr *referenceRequest) getType() uint16 {
	return typeReferenceRequest
}

func (rr *referenceRequest) getPayload() []byte {
	bytes, err := msgpack.Marshal(rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal reference request")
	}
	return bytes
}

func (b *Bounce) handleReferenceRequest(peer string, payload []byte, _ bool) (broadcastable, bool) {
	// Unmarshal the reference request
	var rr referenceRequest
	err := msgpack.Unmarshal(payload, &rr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling reference request")
		return nil, false
	}

	if b.encrypted {
		go b.sendDirect(peer, b.generateEncryptedCatchUpFor(peer, rr))
		return nil, false
	}

	offerable := b.getReferenceOfferFor(peer)
	offeredIDs := referencedIDs(offerable.References)
	requestedIDs := referencedIDs(rr.References)

	deliverable := map[uint16][]uuid.UUID{}
	for _, rf := range referencedFrames {
		deliverable[rf.frameType] = getValidRequestedUUIDs(offeredIDs[rf.frameType], requestedIDs[rf.frameType])
	}

	broadcastables := []broadcastable{}
	for _, rf := range referencedFrames {
		loaded := rf.load(b, deliverable[rf.frameType])
		broadcastables = append(broadcastables, loaded...)
	}

	if len(broadcastables) == 0 {
		return nil, false
	}

	if _, ok := encryptedDeviceCache[peer]; ok {
		// If we're preparing this catch up for an encrypted device, pack it with encrypted versions of the frames
		cuForEncryptedDevice := &catchUp{
			sendables: sortableCatchUpAbles{},
		}

		for _, br := range broadcastables {
			s := b.encryptFrameForDevice(br, peer)
			if s != nil {
				cuForEncryptedDevice.sendables = append(cuForEncryptedDevice.sendables, s)
			} else {
				log.WithFields(log.Fields{
					"id":   br.getID(),
					"type": br.getType(),
				}).Warn("excluding broadcastable from catch up that could not be encrypted")
			}
		}

		go b.sendDirect(peer, cuForEncryptedDevice)
	} else {
		// If we're preparing this catch up for a normal device, pack the frames in directly
		cu := &catchUp{
			sendables: sortableCatchUpAbles{},
		}

		for _, br := range broadcastables {
			s, ok := br.(catchUpAble)
			if !ok {
				log.Error("broadcastable frame is not also a sortableSendable frame")
			}
			cu.sendables = append(cu.sendables, s)
		}

		go b.sendDirect(peer, cu)
	}

	return nil, false
}

// Given two lists of UUIDs, one representing the original offer and the other representing what
// was requested by the peer, separate them into the valid requested UUIDs and the UUIDs that we
// can assume were already delivered because they were not requested.
func getValidRequestedUUIDs(originalOffer []uuid.UUID, requested []uuid.UUID) []uuid.UUID {
	requestedSet := []uuid.UUID{}

	offeredCache := make(map[uuid.UUID]bool)
	for _, offeredID := range originalOffer {
		if _, present := offeredCache[offeredID]; present {
			log.WithFields(log.Fields{
				"id": offeredID,
			}).Warn("duplicate UUID in reference offer generated locally")
			continue
		}
		offeredCache[offeredID] = true
	}

	for _, requestedID := range requested {
		// Make sure that this requested UUID is something we are still offering, and skip it if not.
		// This can sometimes happen for legitimate reasons and is not automatically evidence of bad
		// behavior from a client.
		if _, present := offeredCache[requestedID]; !present {
			log.WithFields(log.Fields{
				"id": requestedID,
			}).Debug("reference request asks for UUID not present in reference offer")
			continue
		}

		// Include the requested UUID in the requested set
		requestedSet = append(requestedSet, requestedID)
	}

	return requestedSet
}
