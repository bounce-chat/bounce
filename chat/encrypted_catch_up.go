package chat

import (
	"crypto/aes"
	"crypto/cipher"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

type encryptedReceive struct {
	ID        uuid.UUID
	Type      uint16
	Payload   []byte
	Encrypter []byte
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
		kek, err := b.generateKEK(f.Encrypter)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error generating key encryption key for encrypted catch up frame")
			continue
		}

		block, err := aes.NewCipher(kek)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error creating block")
			continue
		}
		gcm, err := cipher.NewGCMWithRandomNonce(block)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error creating gcm")
			continue
		}

		decryptedPayload, err := gcm.Open(nil, []byte{}, f.Payload, nil)
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
