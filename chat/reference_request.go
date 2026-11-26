package chat

import (
	"sort"

	"github.com/Basekick-Labs/msgpack/v6"
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

	rd := b.getRemoteDevice(peer)
	if !rd.beginCatchUp() {
		return nil, false
	}
	handedOff := false
	defer func() {
		if !handedOff {
			rd.finishCatchUp()
		}
	}()

	if b.encrypted {
		ecu := b.generateEncryptedCatchUpFor(peer, rr)
		ecu.onComplete = rd.finishCatchUp
		handedOff = true
		go b.sendDirect(peer, ecu)
		return nil, false
	}

	cu := b.generateCatchUpFor(peer, rr)
	if len(cu.Frames) == 0 {
		return nil, false
	}
	cu.onComplete = rd.finishCatchUp
	handedOff = true
	go b.sendDirect(peer, cu)
	return nil, false
}

func (b *Bounce) generateCatchUpFor(peer string, rr referenceRequest) *catchUp {
	offerable := b.getReferenceOfferFor(peer)
	offeredIDs := referencedIDs(offerable.References)
	requestedIDs := referencedIDs(rr.References)

	metadata := []catchUpFrameMetadata{}
	for _, rf := range referencedFrames {
		deliverable := getValidRequestedIDs(offeredIDs[rf.frameType], requestedIDs[rf.frameType])
		loaded := loadCatchUpFrameMetadata(
			b.database.Table(rf.table).Select("id, saved_at"),
			deliverable,
		)
		for i := range loaded {
			loaded[i].Type = rf.frameType
		}
		metadata = append(metadata, loaded...)
	}
	sort.Sort(sortableCatchUpFrameMetadata(metadata))

	encryptedDeviceCacheMutex.Lock()
	_, encrypted := encryptedDeviceCache[peer]
	encryptedDeviceCacheMutex.Unlock()

	cu := &catchUp{}
	total := 0
	owner, ownerKnown := b.peerOwner(peer)
	myID := b.currentUserID()

frames:
	for _, item := range metadata {
		rf := referencedFramesByType[item.Type]
		for _, br := range rf.load(b, []ID{item.ID}) {
			if br.getScope(myID) == scopeSync && (!ownerKnown || owner != myID) {
				log.WithFields(log.Fields{
					"id":   br.getID(),
					"type": br.getType(),
					"peer": peer,
				}).Error("refusing to serve sync-scoped frame to a device that is not a sync device")
				continue
			}

			var s catchUpAble
			if encrypted {
				// An encrypted device is sent a sealed copy of the frame.  It cannot order what it cannot read, so the
				// copies go in the order a plain catch up carries the frames inside them, and the host keeps the order
				// they arrive in.
				ef := b.sealForHost(br, peer)
				if ef == nil {
					log.WithFields(log.Fields{
						"id":   br.getID(),
						"type": br.getType(),
					}).Warn("excluding broadcastable from catch up that could not be encrypted")
					continue
				}
				s = ef
			} else {
				var ok bool
				s, ok = br.(catchUpAble)
				if !ok {
					log.Error("broadcastable frame is not also a catchUpAble frame")
					continue
				}
			}

			payload := s.getPayload()
			if len(cu.Frames) > 0 && total+len(payload) > catchUpPayloadBudget {
				if encrypted {
					// This copy was sealed and is not being sent, so no ack will ever name it
					forgetSealedCopy(s.getID())
				}
				cu.Incomplete = true
				break frames
			}
			cu.Frames = append(cu.Frames, frame{ID: s.getID(), Type: s.getType(), Payload: payload})
			total += len(payload)
		}
	}

	return cu
}

// Given two lists of IDs, one representing the original offer and the other representing what
// was requested by the peer, separate them into the valid requested IDs and the IDs that we
// can assume were already delivered because they were not requested.
func getValidRequestedIDs(originalOffer []ID, requested []ID) []ID {
	requestedSet := []ID{}

	offeredCache := make(map[ID]bool)
	for _, offeredID := range originalOffer {
		if _, present := offeredCache[offeredID]; present {
			log.WithFields(log.Fields{
				"id": offeredID,
			}).Warn("duplicate ID in reference offer generated locally")
			continue
		}
		offeredCache[offeredID] = true
	}

	for _, requestedID := range requested {
		// Make sure that this requested ID is something we are still offering, and skip it if not.
		// This can sometimes happen for legitimate reasons and is not automatically evidence of bad
		// behavior from a client.
		if _, present := offeredCache[requestedID]; !present {
			log.WithFields(log.Fields{
				"id": requestedID,
			}).Debug("reference request asks for ID not present in reference offer")
			continue
		}

		// Include the requested ID in the requested set
		requestedSet = append(requestedSet, requestedID)
	}

	return requestedSet
}
