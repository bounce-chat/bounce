package chat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type encryptedReceive struct {
	ID           uuid.UUID
	Type         uint16
	Payload      []byte
	EncryptedDEK []byte
	PublicKey    []byte
}

type encryptedCatchUp struct {
	Frames       []encryptedReceive
	payload      []byte
	payloadMutex sync.Mutex
}

func (ecu *encryptedCatchUp) getType() uint16 {
	return typeEncryptedCatchUp
}

func (ecu *encryptedCatchUp) getPayload() []byte {
	ecu.payloadMutex.Lock()
	defer ecu.payloadMutex.Unlock()

	if len(ecu.payload) == 0 {
		bytes, err := msgpack.Marshal(ecu)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("cannot msgpack marshal catch up")
		}
		ecu.payload = bytes
	}
	return ecu.payload
}

func (b *Bounce) handleEncryptedCatchUp(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var ecu encryptedCatchUp
	err := msgpack.Unmarshal(payload, &ecu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted catch up")
		return nil, false
	}

	decryptedFrames := []frame{}
	for _, f := range ecu.Frames {
		kek, err := b.generateKEK(f.PublicKey)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error generating key encryption key for encrypted catch up frame")
			continue
		}

		kekBlock, err := aes.NewCipher(kek)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error creating block")
			continue
		}
		kekGCM, err := cipher.NewGCMWithRandomNonce(kekBlock)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error creating gcm")
			continue
		}

		dek, err := kekGCM.Open(nil, []byte{}, f.EncryptedDEK, nil)
		if err != nil {
			log.WithFields(log.Fields{
				"id":    f.ID,
				"type":  f.Type,
				"error": err.Error(),
			}).Error("error decrypting key in encrypted catch up")
			continue
		}

		dekBlock, err := aes.NewCipher(dek)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error creating block")
			continue
		}
		dekGCM, err := cipher.NewGCMWithRandomNonce(dekBlock)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error creating gcm")
			continue
		}

		decryptedPayload, err := dekGCM.Open(nil, []byte{}, f.Payload, nil)
		if err != nil {
			log.WithFields(log.Fields{
				"id":    f.ID,
				"type":  f.Type,
				"error": err.Error(),
			}).Error("error decrypting frame in encrypted catch up")
			continue
		}

		decryptedFrames = append(
			decryptedFrames,
			frame{
				ID:      f.ID,
				Type:    f.Type,
				Payload: decryptedPayload,
			},
		)
	}

	cu := &catchUp{
		Frames: decryptedFrames,
	}
	decryptedCatchUpPayload, err := msgpack.Marshal(cu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("cannot msgpack marshal catch up")
		return nil, false
	}

	return b.handleCatchUp(peer, decryptedCatchUpPayload, true)
}

func (b *Bounce) generateEncryptedCatchUpFor(peer string, rr referenceRequest) *encryptedCatchUp {
	ecu := &encryptedCatchUp{}

	referenceOfferChallengeMutex.Lock()
	peerKey, ok := peerUserKeys[peer]
	referenceOfferChallengeMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Error("cannot generate encrypted catch up for peer with unknown key")
		return ecu
	}

	originalOffer := b.getEncryptedReferenceOfferFor(peer, peerKey)
	allowed := map[uuid.UUID]bool{}
	for _, ref := range originalOffer.References {
		allowed[ref.FrameID] = true
	}

	validIDs := []uuid.UUID{}
	for _, ref := range rr.References {
		if _, ok := allowed[ref.FrameID]; ok {
			validIDs = append(validIDs, ref.FrameID)
		}
	}

	for _, id := range validIDs {
		var ef encryptedFrame
		err := b.database.Preload(clause.Associations).Take(&ef, "id = ?", id).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"id": id,
				}).Error("requested encrypted frame not found")
				continue
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error looking up encrypted frame")
			}
		}

		var desiredRecipient recipient
		found := false
		for _, r := range ef.Recipients {
			if bytes.Equal(r.PublicKey, peerKey) {
				desiredRecipient = r
				found = true
				break
			}
		}

		if !found {
			log.WithFields(log.Fields{
				"id": id,
			}).Error("encrypted frame requested by recipient no in recipient list")
			continue
		}

		ecu.Frames = append(ecu.Frames, encryptedReceive{
			ID:           ef.ID,
			Type:         ef.Type,
			Payload:      ef.Payload,
			EncryptedDEK: desiredRecipient.EncryptedDEK,
			PublicKey:    desiredRecipient.PublicKey,
		})
	}

	return ecu
}
