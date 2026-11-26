package chat

import (
	"errors"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// An add user request accepted frame is sent from the offering device and includes their half of the signatures
// in an add user.  When this is received the requester user can create their half of the signatures and broadcast
// a complete add user.
type addUserRequestAccepted struct {
	Nonce          nonce
	Timestamp      int64
	OfferUser      []byte
	OfferSignature []byte // signature over the mutual addUser digest by the offer user's device
}

func (aura *addUserRequestAccepted) getType() uint16 {
	return typeAddUserRequestAccepted
}

func (aura *addUserRequestAccepted) getPayload() []byte {
	bytes, err := msgpack.Marshal(aura)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal add user request accepted")
	}
	return bytes
}

func (b *Bounce) handleAddUserRequestAccepted(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	if len(payload) > maximumAddUserFramePayload {
		log.WithFields(log.Fields{
			"peer": peer,
			"size": len(payload),
		}).Warn("ignoring add user request accepted larger than maximum allowed payload size")
		return nil, false
	}

	// Make sure we sent a request to this peer recently
	if !pendingAddUserRequests.isExpectedFrom(peer) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring add user request accepted from a peer we did not recently send a request to")
		return nil, false
	}

	// Unmarshal the payload
	var aura addUserRequestAccepted
	err := msgpack.Unmarshal(payload, &aura)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling add user request accepted")
		return nil, false
	}

	if aura.Nonce == noNonce {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring add user request accepted without nonce")
		return nil, false
	}
	if len(aura.OfferUser) > maximumAddUserRecordSize {
		log.WithFields(log.Fields{
			"peer":            peer,
			"offer_user_size": len(aura.OfferUser),
		}).Warn("ignoring add user request accepted with oversized offer user record")
		return nil, false
	}

	// Unmarshal the offer user
	var offerUser user
	err = msgpack.Unmarshal(aura.OfferUser, &offerUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling offer user while handling add user request accepted")
		return nil, false
	}

	// Make sure the fields we store as-is are well formed, and that this profile has a valid device group
	if err := validUserRecord(offerUser); err != nil {
		log.WithFields(log.Fields{
			"peer":    peer,
			"user_id": offerUser.ID,
			"error":   err.Error(),
		}).Warn("add user request accepted contains invalid user record, ignoring")
		return nil, false
	}
	if !b.hasValidDeviceGroup(offerUser) {
		log.Error("add user request accepted contains offer user with invalid device group")
		return nil, false
	}

	// Ensure that the offer user owns the device that is responding
	peerFound := false
	for _, d := range offerUser.Devices {
		if d.Address == peer {
			peerFound = true
			break
		}
	}
	if !peerFound {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("ignoring add user request accepted from device not in the offer user's device group")
		return nil, false
	}

	// Ensure that the offer user and all devices are new
	var existingUser user
	err = b.database.Take(&existingUser, "id = ?", offerUser.ID).Error
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.WithFields(log.Fields{
			"peer":    peer,
			"user_id": offerUser.ID,
		}).Warn("ignoring add user request accepted with conflicting offer user")
		return nil, false
	}
	for _, d := range offerUser.Devices {
		var existingDevice device
		err = b.database.Take(&existingDevice, "id= ? OR address = ?", d.ID, d.Address).Error
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"peer":      peer,
				"device_id": d.ID,
				"address":   d.Address,
			}).Warn("ignoring add user request accepted with conflicting offer user device")
			return nil, false
		}
	}

	// Re-generate our user that was originally sent in the request
	requesterUser, ok := b.currentUser()
	if !ok {
		log.Error("cannot handle add user request accepted when no profile exists")
		return nil, false
	}
	b.attachDeviceRevocations(&requesterUser)
	requesterBytes, err := msgpack.Marshal(requesterUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal requester user while handling add user request accepted")
	}
	// Make sure the offer signature covers the full mutual addUser digest
	signatureDigest := addUserSignatureDigest(aura.Nonce, aura.Timestamp, aura.OfferUser, requesterBytes, peer, b.network.Address())
	ok = b.network.VerifySignature(peer, signatureDigest, aura.OfferSignature)
	if !ok {
		log.Warn("ignoring add user request accepted that contains invalid offer signature")
		return nil, false
	}

	// Create and marshal the new add user
	newUser := addUser{
		Nonce:              aura.Nonce,
		Xor:                xor(requesterUser.ID, offerUser.ID),
		Timestamp:          aura.Timestamp,
		OfferUser:          aura.OfferUser,
		RequesterUser:      requesterBytes,
		OfferDevice:        peer,
		RequesterDevice:    b.network.Address(),
		OfferSignature:     aura.OfferSignature,
		RequesterSignature: b.network.Sign(signatureDigest),
	}
	newUser.ID = newUser.generateID()
	newUserBytes, err := msgpack.Marshal(&newUser)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling add user")
		return nil, false
	}

	// Use the add user handler to save and broadcast
	au, _ := b.handleAddUser(b.network.Address(), newUserBytes, false)
	if au == nil {
		log.Error("failed to create add user frame from add user request accepted")
	} else {
		b.broadcast(au)
	}

	return nil, false
}
