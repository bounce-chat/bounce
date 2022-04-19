package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"github.com/zeebo/blake3"
	"gorm.io/gorm"
)

var groupMessageMutex sync.Mutex

type GroupMessage struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;"`
	SavedAt          int64     `msgpack:"-"`
	WrittenAt        int64
	RetentionSeconds int64 // Number of seconds to retain this message, captures the retention setting from the author's perspective at the time the message was written
	DeleteAt         int64 `msgpack:"-"` // Absolute time at which the messages expires.  Time it was first acked/received + RetentionSeconds
	Read             bool  `msgpack:"-"`
	Undeliverable    bool  `msgpack:"-"` // The message was never delivered to another device and is beyond when we give up including it in reference offers
	Source           uuid.UUID
	Destination      uuid.UUID
	Text             string // TODO: other things that can be in a message, like a reference to an image, audio, video, or file attachment
	Signer           string `msgpack:"-"`
	Payload          []byte `msgpack:"-"`
	Signature        []byte `msgpack:"-"`
	payload          []byte
	payloadMutex     sync.Mutex
}

func (gm *GroupMessage) BeforeCreate(tx *gorm.DB) error {
	gm.SavedAt = time.Now().Unix()
	if len(gm.Payload) == 0 || len(gm.Signature) == 0 || len(gm.Signer) == 0 {
		return errors.New("cannot create a group message without an original signed payload")
	}
	return nil
}

func (gm *GroupMessage) getID() uuid.UUID {
	return gm.ID
}

func (gm *GroupMessage) getScope(_ uuid.UUID) int {
	return scopeGroup
}

func (gm *GroupMessage) getDestination(_ uuid.UUID) uuid.UUID {
	return gm.Destination
}

func (gm *GroupMessage) getType() uint16 {
	return typeGroupMessage
}

func (gm *GroupMessage) getPayload() []byte {
	gm.payloadMutex.Lock()
	defer gm.payloadMutex.Unlock()

	if len(gm.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   gm.Payload,
			Signature: gm.Signature,
			Signer:    gm.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling group message's signed container")
		}
		gm.payload = bytes
	}

	return gm.payload
}

func (gm *GroupMessage) getTimestamp() int64 {
	return 0
}

func (b *bounce) handleGroupMessage(peer string, payload []byte) {
	groupMessageMutex.Lock()
	defer groupMessageMutex.Unlock()

	var sc signedContainer
	err := msgpack.Unmarshal(payload, &sc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling group message signed container")
		return
	}

	hash := blake3.Sum256(sc.Payload)
	if !b.network.VerifySignature(sc.Signer, hash[:], sc.Signature) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("group message received with invalid signature, ignoring")
		return
	}

	var gm GroupMessage
	err = msgpack.Unmarshal(payload, sc.Payload)
	if err != nil {

	}

	// TODO: validate the gm, make sure signer is the author, was delivered by a peer in the group

	b.userInterface.ReceivedGroupMessage(gm)

	gm.Payload = sc.Payload
	gm.Signature = sc.Signature

	err = b.database.Create(&gm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving group message")
	}
}

func (b *bounce) sendGroupMessage(gm *GroupMessage) uuid.UUID {
	gm.ID = uuid.New()
	gm.WrittenAt = time.Now().Unix()
	gm.Read = true
	gm.Source = b.currentUserID()
	gm.RetentionSeconds = 60 * 60 * 24 * 7 // TODO: look up for group

	var err error
	gm.Payload, err = msgpack.Marshal(gm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group message")
	}
	hash := blake3.Sum256(gm.Payload)
	gm.Signature = b.network.Sign(hash[:]) // TODO: just sign the hash of the data for speed reasons https://github.com/lukechampine/blake3 or https://github.com/zeebo/blake3
	gm.Signer = b.network.Address()

	err = b.database.Create(gm).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving group message") // TODO: this breaks fyne
	}

	go b.broadcast(gm)

	return gm.ID
}
