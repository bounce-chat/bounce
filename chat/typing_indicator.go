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
	lastIndicated      int64
	uiIndicatingThread bool
	uiIndicatingButton bool
}

//
// A typing indicator is sent when someone is typing into an entry widget in the UI, to communicate to the other members
// of the thread that a user is currently typing
//
type typingIndicator struct {
	ID              uuid.UUID
	Thread          uuid.UUID
	MessageType     uint16
	Author          uuid.UUID
	timestamp       int64  // Defined when received and not sent over the wire
	Signer          string `msgpack:"-"`
	OriginalPayload []byte `msgpack:"-"`
	Signature       []byte `msgpack:"-"`
	payload         []byte
	payloadMutex    sync.Mutex
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

func (b *bounce) handleTypingIndicator(peer string, payload []byte, catchUp bool) broadcastable {
	// Unmarshal and signature verify the typing indicator
	sc, err := b.unpackSignedContainer(payload)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error unpacking signed container for typing indicator")
		return nil
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

	// Do nothing if we've already seen this typing indicator
	if typingIndicatorAlreadySeen(ti.ID) {
		return nil
	}

	// Make sure that the author of this indicator also signed the message
	signerDevice, ok := b.getDeviceFromAddress(sc.Signer)
	if !ok {
		log.WithFields(log.Fields{
			"peer":   peer,
			"signer": sc.Signer,
		}).Warn("rejecting typing indicator signed by unknown device")
		return nil
	}
	if signerDevice.UserID != ti.Author {
		log.WithFields(log.Fields{
			"peer":   peer,
			"signer": signerDevice.UserID,
			"author": ti.Author,
		}).Warn("rejecting typing indicator not signed by the author")
		return nil
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
		return nil
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
			return nil
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
				return nil
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
			return nil
		}
	} else {
		log.WithFields(log.Fields{
			"thread":       ti.Thread,
			"message_type": ti.MessageType,
			"timestamp":    ti.timestamp,
			"author":       ti.Author,
		}).Warn("received a typing indicator with unsupported message type")
		return nil
	}

	// Refresh the frontend if needed
	b.updateTypingState(ti)

	// Since typing indicators don't use delivery tracking, we don't want to use the standard broadcast function.
	// If we did, the peer we broadcast to would send the frame right back to us, which isn't needed.  Instead,
	// we copy the scoping and broadcast functions, and use all the scoped devices except for this peer.
	go b.manuallySendTypingIndicators(&ti, peer)

	return nil
}

func (b *bounce) manuallySendTypingIndicators(ti *typingIndicator, excludedPeer string) {
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
			if rd.connectedSockets > 0 {
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
			if rd.connectedSockets > 0 {
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
			if rd.connectedSockets > 0 {
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
				if rd.connectedSockets > 0 {
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

func (b *bounce) updateTypingState(ti typingIndicator) {
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

func (b *bounce) updateFrontendTypingIndicators() {
	typingStateMutex.Lock()
	defer typingStateMutex.Unlock()

	for thread, users := range typingState {
		indicatingUsers := map[uuid.UUID]*typingStatus{}

		for u, status := range users {
			if status.uiIndicatingThread && status.lastIndicated < time.Now().Unix()-typingIndicatorDisplayForSeconds {
				status.uiIndicatingThread = false
				b.userInterface.HideTypingIndicatorInHistory(u, thread)
			}
			if !status.uiIndicatingThread && status.lastIndicated > time.Now().Unix()-typingIndicatorDisplayForSeconds {
				status.uiIndicatingThread = true
				if b.typingIndicatorsEnabledForThread(thread) {
					b.userInterface.ShowTypingIndicatorInHistory(u, thread)
				}
			}

			if status.uiIndicatingThread {
				indicatingUsers[u] = status
			}
		}

		if len(indicatingUsers) == 0 {
			b.userInterface.HideTypingIndicatorInButton(thread)
			for _, status := range users {
				status.uiIndicatingButton = false
			}
		} else {
			maxUserID := uuid.Nil
			maxUserTimestamp := int64(0)

			for id, status := range indicatingUsers {
				if status.lastIndicated > maxUserTimestamp {
					maxUserTimestamp = status.lastIndicated
					maxUserID = id
				}
			}

			if !users[maxUserID].uiIndicatingButton {
				if b.typingIndicatorsEnabledForThread(thread) {
					b.userInterface.ShowTypingIndicatorInButton(maxUserID, thread)
				}
				users[maxUserID].uiIndicatingButton = true
			}

			for id, status := range indicatingUsers {
				if id != maxUserID {
					status.uiIndicatingButton = false
				}
			}
		}
	}
}

func (b *bounce) clearUserTypingIndicator(userID, threadID uuid.UUID) {
	typingStateMutex.Lock()
	if _, ok := typingState[threadID]; !ok {
		typingState[threadID] = map[uuid.UUID]*typingStatus{}
	}
	users := typingState[threadID]

	var status *typingStatus
	var ok bool
	if status, ok = users[userID]; !ok {
		status = &typingStatus{}
	}

	status.lastIndicated = 0
	typingStateMutex.Unlock()

	b.updateFrontendTypingIndicators()
}

func (b *bounce) typingIndicatorsEnabledForThread(threadID uuid.UUID) bool {
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
		return b.typingIndicatorsEnabledForUser(xor(threadID, b.currentUserID()))
	}

	log.WithFields(log.Fields{
		"thread_id": threadID,
		"type":      t,
	}).Warn("unknown thread type for typing indicators")
	return false
}

func (b *bounce) typingIndicatorsEnabledForUser(userID uuid.UUID) bool {
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

func (b *bounce) typingInDirectMessage(userID uuid.UUID) {
	if !b.typingIndicatorsEnabledForUser(userID) {
		return
	}
	b.broadcastTypingIndicator(xor(userID, b.currentUserID()), typeDirectMessage)
}

func (b *bounce) typingIndicatorsEnabledForGroup(groupID uuid.UUID) bool {
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

func (b *bounce) typingInGroup(groupID uuid.UUID) {
	if !b.typingIndicatorsEnabledForGroup(groupID) {
		return
	}
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
