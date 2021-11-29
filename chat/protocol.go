package chat

import (
	"errors"

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

var typeDirectMessage = uint16(0)
var typeGroupMessage = uint16(1)
var typeReferenceOffer = uint16(2)
var typeReferenceRequest = uint16(3)
var typeCatchUp = uint16(4)
var typeAck = uint16(5)
var typeKeepAlive = uint16(6)
var typeUpdateLocalDMSettings = uint16(7)

type broadcastable interface {
	getScope() int
	getDestination(myID uuid.UUID) uuid.UUID // A group or user ID depending on the scope
	getType() uint16
	getPayload() []byte
	isAlreadyDeliveredTo(address string) bool
}

func (b *bounce) getHandlers() map[uint16]func(string, []byte) {
	return map[uint16]func(string, []byte){
		typeDirectMessage:         b.handleDirectMessage,
		typeReferenceOffer:        b.handleReferenceOffer,
		typeReferenceRequest:      b.handleReferenceRequest,
		typeCatchUp:               b.handleCatchUp,
		typeAck:                   b.handleAck,
		typeKeepAlive:             b.handleKeepAlive,
		typeUpdateLocalDMSettings: b.handleUpdateLocalDMSettings,
	}
}

func (b *bounce) broadcast(br broadcastable) {
	for _, peer := range b.getBroadcastScope(br) {
		// Async try to write this message to every device that should be written to
		go func(dst chan broadcastable, msg broadcastable) {
			dst <- msg
		}(peer.messages, br)
	}
}

func (b *bounce) getBroadcastScope(br broadcastable) []*remoteDevice {
	scope := br.getScope()

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
		if br.isAlreadyDeliveredTo(dev.Address) {
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
				"scope":        br.getScope(),
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
		if br.isAlreadyDeliveredTo(dev.Address) {
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
				"scope":        br.getScope(),
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
	if br.isAlreadyDeliveredTo(target.Address) {
		return broadcastTargets
	}
	rd := b.getRemoteDevice(target.Address)
	if rd.connectedSockets > 0 {
		broadcastTargets = append(broadcastTargets, rd)
	}

	return broadcastTargets
}

func (b *bounce) getGroupScope(br broadcastable) []*remoteDevice {
	// TODO: look up the group, find all online devices
	return []*remoteDevice{}
}

//
// Send a message to any device that we're connected to right now
//
func (b *bounce) getGlobalScope(br broadcastable) []*remoteDevice {
	broadcastTargets := []*remoteDevice{}
	for _, dev := range b.devicePool.devices {
		//if br.isAlreadyDeliveredTo(dev.Address) {
		//	continue
		//} // TODO: needed?
		if dev.connectedSockets > 0 {
			broadcastTargets = append(broadcastTargets, dev)
		}
	}
	return broadcastTargets
}
