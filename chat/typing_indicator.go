package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/vmihailenco/msgpack/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var indicatorAlreadySeenRetentionSeconds = int64(60)
var sendNewTypingIndicatorCooldownSeconds = int64(3)
var followUpTypingIndicatorUpdateSeconds = 5 * time.Second
var typingIndicatorDisplayForSeconds = int64(3)
var ignoreTypingIndicatorsAfterSeconds = int64(3)

// Typing indicators aren't delivery tracked, but we want to prevent broadcast loops by checking if we've already
// seen them.  This in-memory map stores the ID of a typing indicator, and the time it was seen, so that we can
// prune these records after some time.
var typingIndicatorSeen = map[uuid.UUID]int64{}
var typingIndicatorSeenMutex sync.Mutex

// We onlt want to broadcast a typing indicator for the same thread every so often, so this map stores the last time
// we broadcast one for each thread
var typingIndicatorCooldown = map[uuid.UUID]int64{}
var typingIndicatorCooldownMutex sync.Mutex

// The typing state is a representation of the user interface state of typing indicators.  It is a map from thread ID
// to a map from user IDs to typing statuses.
var typingState = map[uuid.UUID]map[uuid.UUID]*typingStatus{}
var threadTypes = map[uuid.UUID]uint16{}
var typingStateMutex sync.Mutex

// A typing status contains the state of a user as it relates to a thread: where in the UI we're indicating typing,
// and the last time we updated the indication status
type typingStatus struct {
	lastIndicated int64
	uiIndicating  bool
}

// A typing indicator is sent when someone is typing into an entry widget in the UI, to communicate to the other members
// of the thread that a user is currently typing
type typingIndicator struct {
	SignedFrame
	cachedEncoding
	ID          uuid.UUID
	Thread      uuid.UUID
	MessageType uint16
	Author      uuid.UUID
	timestamp   int64 // Defined when received and not sent over the wire
}

func (ti *typingIndicator) getID() uuid.UUID {
	return ti.ID
}

func (ti *typingIndicator) getScope(myID uuid.UUID) int {
	if ti.MessageType == typeDirectMessage {
		if ti.getDestination(myID) == myID {
			return scopeSync
		}
		return scopeUser
	} else if ti.MessageType == typeGroupMessage {
		return scopeGroup
	} else {
		log.WithFields(log.Fields{
			"type": ti.MessageType,
		}).Fatal("unknown message type in typing indicator")
		return scopeSync
	}
}

func (ti *typingIndicator) getDestination(myID uuid.UUID) uuid.UUID {
	if ti.MessageType == typeDirectMessage {
		return xor(myID, ti.Thread)
	} else if ti.MessageType == typeGroupMessage {
		return ti.Thread
	} else {
		log.WithFields(log.Fields{
			"type": ti.MessageType,
		}).Fatal("unknown message type in typing indicator")
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
			Payload:   ti.OriginalPayload,
			Signature: ti.Signature,
			Signer:    ti.Signer,
		})
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error marshalling typing indicator's signed container")
		}
		ti.payload = bytes
	}

	return ti.payload
}

func (ti *typingIndicator) getAuthor() uuid.UUID {
	return ti.Author
}

func (ti *typingIndicator) getTimestamp() int64 {
	return ti.timestamp
}

func (b *Bounce) handleTypingIndicator(peer string, payload []byte, catchUp bool) (broadcastable, bool) {
	// Unmarshal and signature verify the typing indicator
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for typing indicator")
		return nil, false
	}
	var ti typingIndicator
	err = msgpack.Unmarshal(sc.Payload, &ti)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error unmarshalling typing indicator")
	}
	ti.Signer = sc.Signer
	ti.OriginalPayload = sc.Payload
	ti.Signature = sc.Signature

	// Ignore anything from a blocked user
	if blockedUser(ti.getAuthor()) {
		log.WithFields(log.Fields{
			"id":     ti.ID,
			"author": ti.getAuthor(),
		}).Warn("ignoring typing indicator from blocked user")
		return nil, false
	}

	// Do nothing if we've already seen this typing indicator
	if typingIndicatorAlreadySeen(ti.ID) {
		return nil, false
	}

	// Make sure that the author of this indicator also signed the message
	signerDevice, ok := b.getDeviceFromAddress(sc.Signer)
	if !ok {
		log.WithFields(log.Fields{
			"peer":   peer,
			"signer": sc.Signer,
		}).Warn("rejecting typing indicator signed by unknown device")
		return nil, false
	}
	if signerDevice.UserID != ti.Author {
		log.WithFields(log.Fields{
			"peer":   peer,
			"signer": signerDevice.UserID,
			"author": ti.Author,
		}).Warn("rejecting typing indicator not signed by the author")
		return nil, false
	}

	// Assume a typing indicator we receive was broadcast just now and assign the timestamp to now
	ti.timestamp = time.Now().Unix()

	// Look up the device that sent this typing indicator
	peerDevice, ok := b.getDeviceFromAddress(peer)
	if !ok {
		log.WithFields(log.Fields{
			"thread":       ti.Thread,
			"message_type": ti.MessageType,
			"timestamp":    ti.timestamp,
			"author":       ti.Author,
		}).Warn("received a valid typing indicator from unknown device")
		return nil, false
	}

	// Verify the typing indicator according to message type
	if ti.MessageType == typeDirectMessage {
		// DM typing indicators can only come from sync devices or devices belonging to the other user
		if !(b.isSyncDevice(peerDevice) || peerDevice.UserID == xor(ti.Thread, b.currentUserID())) {
			log.WithFields(log.Fields{
				"thread":       ti.Thread,
				"message_type": ti.MessageType,
				"timestamp":    ti.timestamp,
				"author":       ti.Author,
			}).Warn("received a valid typing indicator from device outside scope")
			return nil, false
		}
	} else if ti.MessageType == typeGroupMessage {
		// Group typing indicators must come from a user in the group
		var g group
		err = b.database.Where("id = ?", ti.Thread).First(&g).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"thread":       ti.Thread,
					"message_type": ti.MessageType,
					"timestamp":    ti.timestamp,
					"author":       ti.Author,
				}).Warn("received a valid typing indicator for an unknown group")
				return nil, false
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("database error querying for a group")
			}
		}

		if !b.userIsInGroup(ti.Thread, ti.Author) {
			log.WithFields(log.Fields{
				"thread":       ti.Thread,
				"message_type": ti.MessageType,
				"timestamp":    ti.timestamp,
				"author":       ti.Author,
				"peer":         peer,
				"peer_device":  peerDevice.ID,
				"peer_user":    peerDevice.UserID,
			}).Warn("received a valid typing indicator from user outside of group")
			return nil, false
		}
	} else {
		log.WithFields(log.Fields{
			"thread":       ti.Thread,
			"message_type": ti.MessageType,
			"timestamp":    ti.timestamp,
			"author":       ti.Author,
		}).Warn("received a typing indicator with unsupported message type")
		return nil, false
	}

	// Refresh the frontend if needed
	b.updateTypingState(ti)

	// Since typing indicators don't use delivery tracking, we don't want to use the standard broadcast function.
	// If we did, the peer we broadcast to would send the frame right back to us, which isn't needed.  Instead,
	// we copy the scoping and broadcast functions, and use all the scoped devices except for this peer.
	go b.manuallySendTypingIndicators(&ti, peer)

	return nil, false
}

func (b *Bounce) manuallySendTypingIndicators(ti *typingIndicator, excludedPeer string) {
	scope := ti.getScope(b.currentUserID())
	destination := ti.getDestination(b.currentUserID())
	log.WithFields(log.Fields{
		"type":        typeTypingIndicator,
		"scope":       scope,
		"destination": destination,
	}).Debug("broadcasting frame")

	broadcastTargets := []*remoteDevice{}
	if scope == scopeSync {
		currentUser, exists := b.currentUser()
		if !exists {
			log.Fatal("cannot broadcast typing indicator when no current user exists")
		}

		for _, dev := range currentUser.Devices {
			if dev.Address == b.network.Address() {
				continue
			}
			if dev.Address == excludedPeer {
				continue
			}
			rd := b.getRemoteDevice(dev.Address)
			if rd.connectedSockets.Load() > 0 {
				broadcastTargets = append(broadcastTargets, rd)
			}
		}
	} else if scope == scopeUser {
		// Add their devices
		var destinationUser user
		err := b.database.Preload(clause.Associations).First(&destinationUser, "id = ?", destination).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"scope":       scope,
					"destination": destination,
					"type":        typeTypingIndicator,
				}).Error("user not found when determining broadcast scope for typing indicator")
				return
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error loading user from database")
			}
		}
		for _, dev := range destinationUser.Devices {
			if dev.Address == excludedPeer {
				continue
			}
			rd := b.getRemoteDevice(dev.Address)
			if rd.connectedSockets.Load() > 0 {
				broadcastTargets = append(broadcastTargets, rd)
			}
		}

		// Add our devices
		currentUser, exists := b.currentUser()
		if !exists {
			log.Fatal("cannot broadcast typing indicator when no current user exists")
		}

		for _, dev := range currentUser.Devices {
			if dev.Address == b.network.Address() {
				continue
			}
			if dev.Address == excludedPeer {
				continue
			}
			rd := b.getRemoteDevice(dev.Address)
			if rd.connectedSockets.Load() > 0 {
				broadcastTargets = append(broadcastTargets, rd)
			}
		}
	} else if scope == scopeGroup {
		var destinationGroup group
		err := b.database.Preload("Users.Devices").Preload(clause.Associations).First(&destinationGroup, "id = ?", destination).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				log.WithFields(log.Fields{
					"scope":       scope,
					"destination": destination,
					"type":        typeTypingIndicator,
				}).Error("group not found when determining broadcast scope for typing indicator")
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error loading group from database")
			}
		}
		for _, u := range destinationGroup.Users {
			for _, dev := range u.Devices {
				if dev.Address == b.network.Address() {
					continue
				}
				if dev.Address == excludedPeer {
					continue
				}
				rd := b.getRemoteDevice(dev.Address)
				if rd.connectedSockets.Load() > 0 {
					broadcastTargets = append(broadcastTargets, rd)
				}
			}
		}
	} else {
		log.WithFields(log.Fields{
			"scope": scope,
		}).Fatal("unsupported scope for typing indicators")
	}

	for _, peer := range broadcastTargets {
		go func(dst chan sendable, msg broadcastable) {
			dst <- msg
		}(peer.messages, ti)
	}
}

func typingIndicatorAlreadySeen(id uuid.UUID) bool {
	typingIndicatorSeenMutex.Lock()
	defer typingIndicatorSeenMutex.Unlock()

	// We'll want to keep this pruned to prevent a (small) memory leak,
	// it's easy to just do that here
	for k, v := range typingIndicatorSeen {
		if v < time.Now().Unix()-indicatorAlreadySeenRetentionSeconds {
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

func shouldCooldownTypingIndicator(id uuid.UUID) bool {
	typingIndicatorCooldownMutex.Lock()
	typingIndicatorCooldownMutex.Unlock()

	if lastTime, exists := typingIndicatorCooldown[id]; exists {
		if lastTime < time.Now().Unix()-sendNewTypingIndicatorCooldownSeconds {
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

func (b *Bounce) updateTypingState(ti typingIndicator) {
	typingStateMutex.Lock()

	threadID := ti.Thread
	if ti.MessageType == typeDirectMessage {
		threadID = xor(b.currentUserID(), ti.Thread)
	}

	if _, ok := typingState[threadID]; !ok {
		threadTypes[threadID] = ti.MessageType
		typingState[threadID] = map[uuid.UUID]*typingStatus{}
	}
	users := typingState[threadID]

	if _, ok := users[ti.Author]; !ok {
		users[ti.Author] = &typingStatus{}
	}

	users[ti.Author].lastIndicated = ti.timestamp
	typingStateMutex.Unlock()

	b.updateFrontendTypingIndicators()
	go func() {
		time.Sleep(followUpTypingIndicatorUpdateSeconds)
		b.updateFrontendTypingIndicators()
	}()
}

func (b *Bounce) updateFrontendTypingIndicators() {
	typingStateMutex.Lock()
	defer typingStateMutex.Unlock()

	for thread, users := range typingState {
		indicatingUsers := map[uuid.UUID]*typingStatus{}

		for u, status := range users {
			if status.uiIndicating && status.lastIndicated < time.Now().Unix()-typingIndicatorDisplayForSeconds {
				status.uiIndicating = false
				b.ui.HideTypingIndicator(u, thread)
			}
			if !status.uiIndicating && status.lastIndicated > time.Now().Unix()-typingIndicatorDisplayForSeconds {
				if b.typingIndicatorsEnabledForThread(thread) {
					status.uiIndicating = true
					b.ui.ShowTypingIndicator(u, thread)
				}
			}

			if status.uiIndicating {
				indicatingUsers[u] = status
			}
		}
	}
}

func (b *Bounce) clearUserTypingIndicator(userID, threadID uuid.UUID, threadType uint16) {
	typingStateMutex.Lock()

	_, ok := threadTypes[threadID]
	if !ok {
		threadTypes[threadID] = threadType
	}

	if _, ok = typingState[threadID]; !ok {
		typingState[threadID] = map[uuid.UUID]*typingStatus{}
	}
	users := typingState[threadID]

	var status *typingStatus
	if status, ok = users[userID]; !ok {
		status = &typingStatus{}
	}

	status.lastIndicated = 0
	typingStateMutex.Unlock()

	b.updateFrontendTypingIndicators()
}

func (b *Bounce) typingIndicatorsEnabledForThread(threadID uuid.UUID) bool {
	t, ok := threadTypes[threadID]
	if !ok {
		log.WithFields(log.Fields{
			"thread_id": threadID,
		}).Warn("unknown thread type for typing indicators")
		return false
	}

	if t == typeGroupMessage {
		return b.typingIndicatorsEnabledForGroup(threadID)
	} else if t == typeDirectMessage {
		return b.typingIndicatorsEnabledForUser(threadID)
	}

	log.WithFields(log.Fields{
		"thread_id": threadID,
		"type":      t,
	}).Warn("unknown thread type for typing indicators")
	return false
}

func (b *Bounce) typingIndicatorsEnabledForUser(userID uuid.UUID) bool {
	var u user
	err := b.database.Select("typing_indicators_overridden", "typing_indicators_enabled").First(&u, "id = ?", userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Error("user not found looking up typing indicator settings")
			return false
		} else {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"user_id": userID,
			}).Fatal("database error looking up user typing indicator settings")
		}
	}
	if u.TypingIndicatorsOverridden {
		return u.TypingIndicatorsEnabled
	}

	var defaultSendTypingIndicators bool
	err = b.database.Model(&profileSettings{}).Select("default_send_typing_indicators").Where("user_id = ?", b.currentUserID()).First(&defaultSendTypingIndicators).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("profile settings not found looking up typing indicator settings")
			return false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up default typing indicator settings")
		}
	}
	return defaultSendTypingIndicators
}

func (b *Bounce) TypingInDirectMessage(userID uuid.UUID) {
	if !b.typingIndicatorsEnabledForUser(userID) {
		return
	}
	b.broadcastTypingIndicator(xor(userID, b.currentUserID()), typeDirectMessage)
}

func (b *Bounce) typingIndicatorsEnabledForGroup(groupID uuid.UUID) bool {
	var g group
	err := b.database.Select("typing_indicators_overridden", "typing_indicators_enabled").First(&g, "id = ?", groupID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Error("group not found looking up typing indicator settings")
			return false
		} else {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"group_id": groupID,
			}).Fatal("database error looking up group typing indicator settings")
		}
	}
	if g.TypingIndicatorsOverridden {
		return g.TypingIndicatorsEnabled
	}

	var defaultSendTypingIndicators bool
	err = b.database.Model(&profileSettings{}).Select("default_send_typing_indicators").Where("user_id = ?", b.currentUserID()).First(&defaultSendTypingIndicators).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("profile settings not found looking up typing indicator settings")
			return false
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up default typing indicator settings")
		}
	}
	return defaultSendTypingIndicators
}

func (b *Bounce) TypingInGroup(groupID uuid.UUID) { // TODO: merge with typgin in DM?
	if !b.typingIndicatorsEnabledForGroup(groupID) {
		return
	}
	b.broadcastTypingIndicator(groupID, typeGroupMessage)
}

func (b *Bounce) broadcastTypingIndicator(threadID uuid.UUID, messageType uint16) {
	if shouldCooldownTypingIndicator(threadID) {
		return
	}

	ti := &typingIndicator{
		ID:          uuid.New(),
		Thread:      threadID,
		MessageType: messageType,
		Author:      b.currentUserID(),
	}

	var err error
	ti.OriginalPayload, err = msgpack.Marshal(ti)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("unable to marshal typing indicator")
	}

	sc := b.createSignedContainer(ti.OriginalPayload)
	ti.Signer = sc.Signer
	ti.Signature = sc.Signature

	// Record that we've already seen this ID before we broadcast it to avoid a broadcast loop
	typingIndicatorSeenMutex.Lock()
	typingIndicatorSeen[ti.ID] = time.Now().Unix()
	typingIndicatorSeenMutex.Unlock()

	b.broadcast(ti)
}
