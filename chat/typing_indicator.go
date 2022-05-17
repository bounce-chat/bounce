package chat

import (
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
)

var typingIndicatorSeen = map[uuid.UUID]int64{}
var typingIndicatorSeenMutex sync.Mutex

func typingIndicatorAlreadySeen(id uuid.UUID) bool {
	typingIndicatorSeenMutex.Lock()
	defer typingIndicatorSeenMutex.Unlock()

	// We'll want to keep this pruned to prevent a (small) memory leak,
	// it's easy to just do that here
	for k, v := range typingIndicatorSeen {
		if v < time.Now().Unix()-60 {
			delete(typingIndicatorSeen, k)
		}
	}

	if _, exists := typingIndicatorSeen[id]; exists {
		return true
	} else {
		typingIndicatorSeen[id] = time.Now().Unix()
		return false
	}
}

var typingIndicatorCooldown = map[uuid.UUID]int64{}
var typingIndicatorCooldownMutex sync.Mutex

func shouldCooldownTypingIndicator(id uuid.UUID) bool {
	typingIndicatorCooldownMutex.Lock()
	typingIndicatorCooldownMutex.Unlock()

	if lastTime, exists := typingIndicatorCooldown[id]; exists {
		if lastTime < time.Now().Unix()-3 {
			typingIndicatorCooldown[id] = time.Now().Unix()
			return false
		} else {
			return true
		}
	} else {
		typingIndicatorCooldown[id] = time.Now().Unix()
		return false
	}
}

type typingIndicator struct {
	ID           uuid.UUID
	Thread       uuid.UUID
	MessageType  uint16
	Timestamp    int64
	Author       uuid.UUID
	Signer       string `msgpack:"-"`
	Payload      []byte `msgpack:"-"` // TODO: rename to marshalled or something
	Signature    []byte `msgpack:"-"`
	payload      []byte
	payloadMutex sync.Mutex
}

func (ti *typingIndicator) getID() uuid.UUID {
	return ti.ID
}

func (ti *typingIndicator) getScope(_ uuid.UUID) int {
	if ti.MessageType == typeDirectMessage {
		return scopeUser
	} else if ti.MessageType == typeGroupMessage {
		return scopeGroup
	} else {
		// This shouldn't be possible, but if a malformed
		// typing indicator is being broadcast, make sure it
		// is not sent to other users
		return scopeSync
	}
}

func (ti *typingIndicator) getDestination(myID uuid.UUID) uuid.UUID {
	if ti.MessageType == typeDirectMessage {
		return xor(myID, ti.Thread)
	} else if ti.MessageType == typeGroupMessage {
		return ti.Thread
	} else {
		// This shouldn't be possible, but if a malformed
		// typing indicator is being broadcast, make sure it
		// is not sent to other users
		return uuid.Nil
	}

}

func (ti *typingIndicator) getType() uint16 {
	return typeTypingIndicator
}

func (ti *typingIndicator) getPayload() []byte {
	ti.payloadMutex.Lock()
	defer ti.payloadMutex.Unlock()

	if len(ti.payload) == 0 {
		bytes, err := msgpack.Marshal(signedContainer{
			Payload:   ti.Payload,
			Signature: ti.Signature,
			Signer:    ti.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling group message's signed container")
		}
		ti.payload = bytes
	}

	return ti.payload
}

func (b *bounce) handleTypingIndicator(peer string, payload []byte) {
	var sc signedContainer
	err := msgpack.Unmarshal(payload, &sc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unmarshalling typing indicator signed container")
		return
	}

	if !b.validSignedContainer(sc) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("typing indicator received with invalid signature, ignoring")
		return
	}

	var ti typingIndicator
	err = msgpack.Unmarshal(sc.Payload, &ti)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error unmarshalling typing indicator")
	}

	if typingIndicatorAlreadySeen(ti.ID) {
		return
	}

	// TODO: validate that the timestamp is recent, the author and the peer share a group if this is for a group,
	// or that the peer is sync or the other user if this is DM

	log.WithFields(log.Fields{
		"id":           ti.ID,
		"thread":       ti.Thread,
		"message_type": ti.MessageType,
		"timestamp":    ti.Timestamp,
		"author":       ti.Author,
	}).Info("got a typing indicator")

	// TODO: inform the UI to display the indicator (unless it's already running, track state in the chat package)

	ti.Signer = sc.Signer
	ti.Payload = sc.Payload
	ti.Signature = sc.Signature
	go b.broadcast(&ti)
}

//
// UI Handlers
//

func (b *bounce) TypingInDirectMessage(userID uuid.UUID) {
	b.broadcastTypingIndicator(xor(userID, b.currentUserID()), typeDirectMessage)
}

func (b *bounce) TypingInGroup(groupID uuid.UUID) {
	b.broadcastTypingIndicator(groupID, typeGroupMessage)
}

func (b *bounce) broadcastTypingIndicator(threadID uuid.UUID, messageType uint16) {
	if shouldCooldownTypingIndicator(threadID) {
		return
	}

	ti := &typingIndicator{
		ID:          uuid.New(),
		Thread:      threadID,
		MessageType: messageType,
		Timestamp:   time.Now().Unix(),
		Author:      b.currentUserID(),
	}

	var err error
	ti.Payload, err = msgpack.Marshal(ti)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("unable to marshal typing indicator")
	}

	sc := b.createSignedContainer(ti.Payload)
	ti.Signer = sc.Signer
	ti.Signature = sc.Signature

	// Record that we've already seen this ID before we broadcast it to avoid a broadcast loop
	typingIndicatorSeenMutex.Lock()
	typingIndicatorSeen[ti.ID] = time.Now().Unix()
	typingIndicatorSeenMutex.Unlock()

	go b.broadcast(ti)
}
