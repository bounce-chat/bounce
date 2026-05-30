package chat

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"sort"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type encryptedReceive struct {
	ID               uuid.UUID
	Type             uint16
	Payload          []byte
	EncryptedDEK     []byte
	EncrypterKey     []byte
	EncrypterAddress string
	UseAddress       bool
	OldKeyHash       string // Indicates that a previously used key will be required to decrypt this frame
	savedAt          int64
}

func (er encryptedReceive) getID() uuid.UUID {
	return er.ID
}

func (er encryptedReceive) getPayload() []byte {
	bytes, err := msgpack.Marshal(&er)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal encrypted receive")
	}
	return bytes
}

func (er encryptedReceive) getType() uint16 {
	return typeEncryptedReceive
}

func (er encryptedReceive) getSavedAt() int64 {
	return er.savedAt
}

type encryptedCatchUp struct {
	Frames sortableEncryptedReceives
}

type sortableEncryptedReceives []encryptedReceive

func (ser sortableEncryptedReceives) Len() int {
	return len(ser)
}
func (ser sortableEncryptedReceives) Swap(i, j int) {
	ser[i], ser[j] = ser[j], ser[i]
}
func (ser sortableEncryptedReceives) Less(i, j int) bool {
	if ser[i].savedAt == ser[j].savedAt {
		iType := ser[i].Type
		jType := ser[j].Type

		iOrder, ok := catchUpOrder[iType]
		if !ok {
			log.WithFields(log.Fields{
				"type": iType,
			}).Error("encrypted catch up type cannot be ordered")
			return false
		}
		jOrder, ok := catchUpOrder[jType]
		if !ok {
			log.WithFields(log.Fields{
				"type": jType,
			}).Error("encrypted catch up type cannot be ordered")
			return false
		}

		return iOrder < jOrder
	}

	return ser[i].savedAt < ser[j].savedAt
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

	decryptedFrames := []frame{}
	keyHistory := b.encryptionKeyHistory()
	for _, er := range ecu.Frames {
		decrypted, err := b.decryptEncryptedReceive(er, keyHistory)
		if err == nil {
			decryptedFrames = append(decryptedFrames, decrypted)
		}
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

func (b *Bounce) handleEncryptedReceive(peer string, payload []byte, _ bool) (broadcastable, bool) {
	var er encryptedReceive
	err := msgpack.Unmarshal(payload, &er)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling encrypted receive")
		return nil, false
	}

	decrypted, err := b.decryptEncryptedReceive(er, b.encryptionKeyHistory())
	if err != nil {
		return nil, false
	}

	handlers := b.getHandlers(false)
	handler, ok := handlers[decrypted.Type]
	if ok {
		br, _ := handler(peer, decrypted.Payload, false)
		if br != nil {
			go b.sendAck(peer, decrypted.Type, decrypted.ID)
			err := b.database.Clauses(clause.OnConflict{DoNothing: true}).Create(&deliveryRecord{
				Destination: peer,
				FrameID:     decrypted.ID,
				FrameType:   decrypted.Type,
			}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"id":    decrypted.ID,
					"type":  decrypted.Type,
					"peer":  peer,
				}).Error("error saving delivery tracking for encrypted receive")
			}
		}
	} else {
		log.WithFields(log.Fields{
			"type": decrypted.Type,
		}).Error("encrypted receive type does not have a handler")
	}

	return nil, false
}

func (b *Bounce) decryptEncryptedReceive(f encryptedReceive, encryptionKeyHistory map[string][]byte) (frame, error) {
	var kek []byte
	var empty frame
	var err error

	if f.UseAddress {
		myDevice, ok := b.getDeviceFromAddress(b.network.Address())
		if !ok {
			log.Warn("my device not found when handling encrypted receive")
		}
		recipientDevice, ok := b.getDeviceFromAddress(f.EncrypterAddress)
		if !ok {
			log.WithFields(log.Fields{
				"address": f.EncrypterAddress,
			}).Error("encrypter device not found for encrypted receive")
		}

		kek, err = b.generateKEKFromPrivateKey(myDevice.ECDHPrivateKey, recipientDevice.ECDHPublicKey)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error generating key encryption key from device for encrypted catch up frame")
			return empty, err
		}
	} else if f.OldKeyHash != "" {
		oldPrivateKey, ok := encryptionKeyHistory[f.OldKeyHash]
		if !ok {
			log.WithFields(log.Fields{
				"id":      f.ID,
				"type":    f.Type,
				"old_key": f.OldKeyHash,
			}).Error("unable to find encryption key for encrypted receive that specifies and older key")
			return empty, errors.New("encryption key not found")
		}
		kek, err = b.generateKEKFromPrivateKey(oldPrivateKey, f.EncrypterKey)
		if err != nil {
			log.WithFields(log.Fields{
				"old_key": f.OldKeyHash,
				"error":   err.Error(),
			}).Error("error generating key encryption key for encrypted catch up frame")
			return empty, err
		}
	} else {
		kek, err = b.generateKEK(f.EncrypterKey)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error generating key encryption key for encrypted catch up frame")
			return empty, err
		}
	}

	kekBlock, err := aes.NewCipher(kek)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating block")
		return empty, err
	}
	kekGCM, err := cipher.NewGCMWithRandomNonce(kekBlock)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating gcm")
		return empty, err
	}

	dek, err := kekGCM.Open(nil, []byte{}, f.EncryptedDEK, nil)
	if err != nil {
		log.WithFields(log.Fields{
			"id":    f.ID,
			"type":  f.Type,
			"error": err.Error(),
		}).Error("error decrypting key in encrypted catch up")
		return empty, err
	}

	dekBlock, err := aes.NewCipher(dek)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating block")
		return empty, err
	}
	dekGCM, err := cipher.NewGCMWithRandomNonce(dekBlock)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating gcm")
		return empty, err
	}

	decryptedPayload, err := dekGCM.Open(nil, []byte{}, f.Payload, nil)
	if err != nil {
		log.WithFields(log.Fields{
			"id":    f.ID,
			"type":  f.Type,
			"error": err.Error(),
		}).Error("error decrypting frame in encrypted catch up")
		return empty, err
	}

	return frame{
		ID:      f.ID,
		Type:    f.Type,
		Payload: decryptedPayload,
	}, nil
}

func (b *Bounce) generateEncryptedCatchUpFor(peer string, rr referenceRequest) *encryptedCatchUp {
	ecu := &encryptedCatchUp{}

	peerUserKeyMutex.Lock()
	peerKey, ok := peerUserKeys[peer]
	peerUserKeyMutex.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Error("cannot generate encrypted catch up for peer with unknown key")
		return ecu
	}

	var au authorizedUser
	err := b.database.First(&au).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting authorized user while generating encrypted catch up")
	}

	allowedRecipientKeys := [][]byte{peerKey}
	originalOffer := b.getEncryptedReferenceOfferFor(peer, peerKey)
	// If this public key belongs to our authorized user, then also include anything we have for their old keys
	if bytes.Equal(peerKey, au.PublicKey) {
		var oldKeys []oldKey
		err = b.database.Where("authorized_user_id = ?", au.ID).Find(&oldKeys).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error getting all old keys")
		}

		for _, pastKey := range oldKeys {
			allowedRecipientKeys = append(allowedRecipientKeys, pastKey.PublicKey)
			okEro := b.getEncryptedReferenceOfferFor(peer, pastKey.PublicKey)
			if okEro != nil {
				originalOffer.References = append(originalOffer.References, okEro.References...)
			}
		}
	}

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
			for _, allowedKey := range allowedRecipientKeys {
				if bytes.Equal(r.PublicKey, allowedKey) {
					desiredRecipient = r
					found = true
					break
				}
			}
			if found {
				break
			}
		}

		var desiredDeviceRecipient deviceRecipient
		encryptedToDevice := false
		if !found {
			for _, dr := range ef.DeviceRecipients {
				if dr.RecipientAddress == peer {
					found = true
					desiredDeviceRecipient = dr
					encryptedToDevice = true
					break
				}
			}
		}

		if !found {
			log.WithFields(log.Fields{
				"id": id,
			}).Error("encrypted frame requested by recipient no in recipient list")
			continue
		}

		var er encryptedReceive
		if !encryptedToDevice {
			er = encryptedReceive{
				ID:           ef.ID,
				Type:         ef.Type,
				Payload:      ef.Payload,
				EncryptedDEK: desiredRecipient.EncryptedDEK,
				EncrypterKey: desiredRecipient.EncrypterKey,
				UseAddress:   false,
				savedAt:      ef.Timestamp,
			}

			// If this recipient differs from the current key for our user, include the key hash of the appropriate key.  We also
			// attach this to updateDevices, in case we're revoking a device and will be changing keys at the same time.
			if !bytes.Equal(peerKey, desiredRecipient.PublicKey) || ef.Type == typeUpdateDevice {
				er.OldKeyHash = hashString(blake3.Sum256(desiredRecipient.PublicKey))
			}
		} else {
			er = encryptedReceive{
				ID:               ef.ID,
				Type:             ef.Type,
				Payload:          ef.Payload,
				EncryptedDEK:     desiredDeviceRecipient.EncryptedDEK,
				EncrypterAddress: desiredDeviceRecipient.Counterparty,
				UseAddress:       true,
				savedAt:          ef.Timestamp,
			}
		}

		ecu.Frames = append(ecu.Frames, er)
	}

	sort.Sort(ecu.Frames)

	return ecu
}
