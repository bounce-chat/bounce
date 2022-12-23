package chat

import (
	"errors"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var scopeSync = 0
var scopeUser = 1
var scopeGroup = 2
var scopeGlobal = 3
var scopeDevice = 4
var scopeOverlap = 5

var typeDirectMessage = uint16(0)
var typeGroupMessage = uint16(1)
var typeReferenceOffer = uint16(2)
var typeReferenceRequest = uint16(3)
var typeCatchUp = uint16(4)
var typeAck = uint16(5)
var typeKeepAlive = uint16(6)

//var typeUpdateLocalDMSettings = uint16(7)
var typeSyncDeviceRequest = uint16(8)
var typeSyncDeviceRequestRejected = uint16(9)
var typeSyncDeviceRequestAccepted = uint16(10)
var typeDevice = uint16(11)
var typeUser = uint16(12)
var typeUpdateDM = uint16(13)
var typeGroupCreation = uint16(14)
var typeUpdateGroup = uint16(15)
var typeTypingIndicator = uint16(16)

type broadcastable interface {
	getID() uuid.UUID
	getScope(myID uuid.UUID) int
	getDestination(myID uuid.UUID) uuid.UUID
	getType() uint16
	getPayload() []byte
}

type sortableBroadcastable interface {
	broadcastable
	getTimestamp() int64
}

type sortableBroadcastables []sortableBroadcastable

func (sbrs sortableBroadcastables) Len() int {
	return len(sbrs)
}
func (sbrs sortableBroadcastables) Swap(i, j int) {
	sbrs[i], sbrs[j] = sbrs[j], sbrs[i]
}
func (sbrs sortableBroadcastables) Less(i, j int) bool {
	return sbrs[i].getTimestamp() < sbrs[j].getTimestamp()
}

func (b *bounce) getHandlers() map[uint16]func(string, []byte) {
	return map[uint16]func(string, []byte){
		typeDirectMessage:             b.handleDirectMessage,
		typeGroupMessage:              b.handleGroupMessage,
		typeReferenceOffer:            b.handleReferenceOffer,
		typeReferenceRequest:          b.handleReferenceRequest,
		typeCatchUp:                   b.handleCatchUp,
		typeAck:                       b.handleAck,
		typeKeepAlive:                 b.handleKeepAlive,
		typeSyncDeviceRequest:         b.handleSyncDeviceRequest,
		typeSyncDeviceRequestRejected: b.handleSyncDeviceRequestRejected,
		typeSyncDeviceRequestAccepted: b.handleSyncDeviceRequestAccepted,
		typeDevice:                    b.handleDevice,
		typeUser:                      b.handleUser,
		typeUpdateDM:                  b.handleUpdateDM,
		typeGroupCreation:             b.handleGroupCreation,
		typeUpdateGroup:               b.handleUpdateGroup,
		typeTypingIndicator:           b.handleTypingIndicator,
	}
}

func (b *bounce) broadcast(br broadcastable) {
	log.WithFields(log.Fields{
		"type":        br.getType(),
		"scope":       br.getScope(b.currentUserID()),
		"destination": br.getDestination(b.currentUserID()),
	}).Debug("broadcasting frame")
	for _, peer := range b.getBroadcastScope(br) {
		// Async try to write this message to every device that should be written to
		go func(dst chan broadcastable, msg broadcastable) {
			dst <- msg
		}(peer.messages, br)
	}
}

// Can only be used with device-scoped frames
func (b *bounce) broadcastUntilDelivered(br broadcastable) {
	giveUpTime := time.Now().Add(5 * time.Minute)

	if br.getScope(b.currentUserID()) != scopeDevice {
		log.WithFields(log.Fields{
			"frame_id":    br.getID(),
			"frame_type":  br.getType(),
			"frame_scope": br.getScope(b.currentUserID()),
		}).Fatal("cannot use broadcastUntilDelivered on frames that are not device scoped")
	}

	// Look up the address of the device we're broadcasting to
	var dev device
	err := b.database.First(&dev, "id = ?", br.getDestination(b.currentUserID())).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"device_id": br.getDestination(b.currentUserID()),
			}).Error("cannot broadcast to an unknown device")
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error looking up device")
		}
	}

	for {
		b.broadcast(br)
		time.Sleep(30 * time.Second) // TODO: derive from message size?

		if b.isDeliveredTo(br, dev.Address) {
			// we got the request, our offer was delivered
			return
		}
		if time.Now().After(giveUpTime) {
			log.WithFields(log.Fields{
				"id":          br.getID(),
				"destination": br.getDestination(b.currentUserID()),
			}).Warn("gave up attempting to deliver catch up")
			return
		}
	}

}

func (b *bounce) getBroadcastScope(br broadcastable) []*remoteDevice {
	scope := br.getScope(b.currentUserID())

	if scope == scopeSync {
		return b.getSyncScope(br)
	} else if scope == scopeUser {
		return b.getUserScope(br)
	} else if scope == scopeDevice {
		return b.getDeviceScope(br)
	} else if scope == scopeGroup {
		return b.getGroupScope(br)
	} else if scope == scopeGlobal {
		return b.getGlobalScope(br)
	} else if scope == scopeOverlap {
		return b.getOverlapScope(br)
	} else {
		log.WithFields(log.Fields{
			"destination": br.getDestination(b.currentUserID()),
			"type":        br.getType(),
			"scope":       scope,
		}).Fatal("unknown broadcast scope")
	}

	return []*remoteDevice{}
}

func (b *bounce) getSyncScope(br broadcastable) []*remoteDevice {
	currentUser, exists := b.currentUser()

	if !exists {
		// TODO: fatal?
	}

	broadcastTargets := []*remoteDevice{}
	for _, dev := range currentUser.Devices {
		if dev.Address == b.network.Address() {
			continue
		}
		if b.isDeliveredTo(br, dev.Address) {
			continue
		}
		rd := b.getRemoteDevice(dev.Address)
		if rd.connectedSockets > 0 {
			broadcastTargets = append(broadcastTargets, rd)
		}
	}
	return broadcastTargets
}

func (b *bounce) getUserScope(br broadcastable) []*remoteDevice {
	broadcastTargets := []*remoteDevice{}

	if b.currentUserID() == br.getDestination(b.currentUserID()) {
		log.WithFields(log.Fields{
			"type": br.getType(),
		}).Warn("a user-scoped message has a sync destination, using sync scope")
		return b.getSyncScope(br)
	}

	// Add any devices that are owned by the destination user that are online
	var destinationUser user
	err := b.database.Preload(clause.Associations).First(&destinationUser, "id = ?", br.getDestination(b.currentUserID())).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"scope":        br.getScope(b.currentUserID()),
				"destinations": br.getDestination(b.currentUserID()),
				"type":         br.getType(),
			}).Error("user not found when determining broadcast scope for message")
			return broadcastTargets
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading user from database")
		}
	}
	for _, dev := range destinationUser.Devices {
		if b.isDeliveredTo(br, dev.Address) {
			continue
		}
		rd := b.getRemoteDevice(dev.Address)
		if rd.connectedSockets > 0 {
			broadcastTargets = append(broadcastTargets, rd)
		}
	}

	// Add any sync devices that are online
	broadcastTargets = append(broadcastTargets, b.getSyncScope(br)...)

	return broadcastTargets
}

func (b *bounce) getDeviceScope(br broadcastable) []*remoteDevice {
	destination := br.getDestination(b.currentUserID())
	broadcastTargets := []*remoteDevice{}

	var target device
	err := b.database.First(&target, "id = ?", destination).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"scope":        br.getScope(b.currentUserID()),
				"destinations": destination,
				"type":         br.getType(),
			}).Error("device not found when determining broadcast scope for message")
			return broadcastTargets
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading device from database")
		}
	}
	if b.isDeliveredTo(br, target.Address) {
		return broadcastTargets
	}
	rd := b.getRemoteDevice(target.Address)
	if rd.connectedSockets > 0 {
		broadcastTargets = append(broadcastTargets, rd)
	}

	return broadcastTargets
}

//
// Get any devices that we are connected to that belong to any members of a group, including ourself
//
func (b *bounce) getGroupScope(br broadcastable) []*remoteDevice {
	broadcastTargets := []*remoteDevice{}

	var destinationGroup group
	err := b.database.Preload("Users.Devices").Preload(clause.Associations).First(&destinationGroup, "id = ?", br.getDestination(b.currentUserID())).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"scope":        br.getScope(b.currentUserID()),
				"destinations": br.getDestination(b.currentUserID()),
				"type":         br.getType(),
			}).Error("group not found when determining broadcast scope for message")
			return broadcastTargets
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading group from database")
		}
	}
	for _, u := range destinationGroup.Users {
		for _, dev := range u.Devices {
			if b.isDeliveredTo(br, dev.Address) {
				continue
			}
			rd := b.getRemoteDevice(dev.Address)
			if rd.connectedSockets > 0 {
				broadcastTargets = append(broadcastTargets, rd)
			}
		}
	}

	return broadcastTargets
}

//
// Send a message to any device that we're connected to right now
//
func (b *bounce) getGlobalScope(br broadcastable) []*remoteDevice {
	broadcastTargets := []*remoteDevice{}
	for address, dev := range b.devicePool.devices {
		if _, exists := b.getDeviceFromAddress(address); !exists {
			// Skip connections in the device pool if we don't have a device saved for them
			continue
		}
		if b.isDeliveredTo(br, address) {
			continue
		}
		if dev.connectedSockets > 0 {
			broadcastTargets = append(broadcastTargets, dev)
		}
	}
	return broadcastTargets
}

func (b *bounce) getOverlapScope(br broadcastable) []*remoteDevice { // TODO: better name for this?
	broadcastTargets := []*remoteDevice{}
	// So that we can tell third party A something we learned about third party B,
	// like new devices or profile updates
	// br.getDestination() describes a user ID, get all of the devices that share any group with the user,
	// as well as this user's devices and our sync devices
	return broadcastTargets
}
