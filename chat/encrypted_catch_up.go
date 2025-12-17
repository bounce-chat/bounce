package chat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"

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
	EncrypterKey []byte
}

type encryptedCatchUp struct {
	DEKs   []discloseDEK
	Frames []encryptedReceive
}

func (ecu *encryptedCatchUp) getType() uint16 {
	return typeEncryptedCatchUp
}

func (ecu *encryptedCatchUp) getPayload() []byte {
	bytes, err := msgpack.Marshal(ecu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal catch up")
	}
	return bytes
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

	for _, dd := range ecu.DEKs {
		kek, err := b.generateKEK(dd.EncrypterKey)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error generating key encryption key for disclosed dek in encrypted catch up frame")
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

		dek, err := kekGCM.Open(nil, []byte{}, dd.EncryptedDEK, nil)
		if err != nil {
			log.WithFields(log.Fields{
				"id":    dd.ID,
				"error": err.Error(),
			}).Error("error decrypting disclosed dek in encrypted catch up")
			continue
		}

		err = b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&dataEncryptionKey{ID: dd.ID, Key: dek}).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error saving data encryption key")
		}
	}

	decryptedFrames := []frame{}
	for _, f := range ecu.Frames {
		kek, err := b.generateKEK(f.EncrypterKey)
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

		if storeDEK(f.Type) {
			err = b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&dataEncryptionKey{ID: f.ID, Key: dek}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error saving data encryption key")
			}
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
	requested := map[uuid.UUID]bool{}
	for _, ref := range rr.References {
		requested[ref.FrameID] = true
	}

	validIDs := []uuid.UUID{}
	for _, ref := range rr.References {
		if _, ok := allowed[ref.FrameID]; ok {
			validIDs = append(validIDs, ref.FrameID)
		}
	}

	for _, ref := range originalOffer.References {
		if _, ok := requested[ref.FrameID]; !ok {
			if storeDEK(ref.Type) {
				var r recipient
				err := b.database.Where("encrypted_frame_id = ? AND public_key = ?", ref.FrameID, peerKey).Find(&r).Error
				if err != nil {
					if errors.Is(err, gorm.ErrRecordNotFound) {
						log.WithFields(log.Fields{
							"frame_id": ref.FrameID,
						}).Error("offered frame to peer without recipient")
						continue
					} else {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Fatal("database error looking up recipient")
					}
				}

				ecu.DEKs = append(ecu.DEKs, discloseDEK{
					ID:           ref.FrameID,
					EncryptedDEK: r.EncryptedDEK,
					EncrypterKey: r.EncrypterKey,
				})
			}
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
			EncrypterKey: desiredRecipient.EncrypterKey,
		})
	}

	return ecu
}
