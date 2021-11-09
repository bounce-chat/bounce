package chat

import (
	"errors"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var SYNC_SCOPE = 0 // TODO: unexport these
var USER_SCOPE = 1
var GROUP_SCOPE = 2
var GLOBAL_SCOPE = 3
var DEVICE_SCOPE = 4

var TYPE_DIRECT_MESSAGE = uint16(0)
var TYPE_GROUP_MESSAGE = uint16(1)
var TYPE_REFERENCE_OFFER = uint16(2)
var TYPE_REFERENCE_REQUEST = uint16(3)
var TYPE_CATCH_UP = uint16(4)
var TYPE_ACK = uint16(5)
var TYPE_KEEP_ALIVE = uint16(6)

type broadcastable interface {
	getScope() int
	getDestination() uuid.UUID // A group or user ID depending on the scope
	getType() uint16
	getPayload() []byte
	isAlreadyDeliveredTo(address string) bool
}

func (bounce *Bounce) getHandlers() map[uint16]func(string, []byte) {
	return map[uint16]func(string, []byte){
		TYPE_DIRECT_MESSAGE:    bounce.handleDirectMessage,
		TYPE_REFERENCE_OFFER:   bounce.handleReferenceOffer,
		TYPE_REFERENCE_REQUEST: bounce.handleReferenceRequest,
		TYPE_CATCH_UP:          bounce.handleCatchUp,
		TYPE_ACK:               bounce.handleAck,
		TYPE_KEEP_ALIVE:        bounce.handleKeepAlive,
	}
}

func (bounce *Bounce) broadcast(b broadcastable) {
	for _, peer := range bounce.getBroadcastScope(b) {
		// Async try to write this message to every device that should be written to
		go func(dst chan broadcastable, msg broadcastable) {
			dst <- msg
		}(peer.messages, b)
	}
}

func (bounce *Bounce) getBroadcastScope(b broadcastable) []*remoteDevice {
	scope := b.getScope()

	if scope == SYNC_SCOPE {
		return bounce.getSyncScope()
	} else if scope == USER_SCOPE {
		return bounce.getUserScope(b)
	} else if scope == DEVICE_SCOPE {
		return bounce.getDeviceScope(b)
	} else if scope == GROUP_SCOPE {
		return bounce.getGroupScope(b)
	} else if scope == GLOBAL_SCOPE {
		return bounce.getGlobalScope()
	} else {
		log.WithFields(log.Fields{
			"destination": b.getDestination(),
			"type":        b.getType(),
			"scope":       scope,
		}).Fatal("unknown broadcast scope")
	}

	return []*remoteDevice{}
}

func (bounce *Bounce) getSyncScope() []*remoteDevice {
	currentUser, exists := bounce.currentUser()

	if !exists {
		// TODO: fatal?
	}

	broadcastTargets := []*remoteDevice{}
	for _, dev := range currentUser.Devices {
		if dev.Address == bounce.network.Address() {
			continue
		}
		rd := bounce.getRemoteDevice(dev.Address)
		if rd.connectedSockets > 0 {
			broadcastTargets = append(broadcastTargets, rd)
		}
	}
	return broadcastTargets
}

func (bounce *Bounce) getUserScope(b broadcastable) []*remoteDevice {
	broadcastTargets := []*remoteDevice{}

	// Add any devices that are owned by the destination user that are online
	var destinationUser user
	err := bounce.database.Preload(clause.Associations).First(&destinationUser, "id = ?", b.getDestination()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"scope":        b.getScope(),
				"destinations": b.getDestination(),
				"type":         b.getType(),
			}).Error("user not found when determining broadcast scope for message")
			return broadcastTargets
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading user from database")
		}
	}
	for _, dev := range destinationUser.Devices {
		if b.isAlreadyDeliveredTo(dev.Address) {
			continue
		}
		rd := bounce.getRemoteDevice(dev.Address)
		if rd.connectedSockets > 0 {
			broadcastTargets = append(broadcastTargets, rd)
		}
	}

	// Add any sync devices that are online
	broadcastTargets = append(broadcastTargets, bounce.getSyncScope()...)

	return broadcastTargets
}

func (bounce *Bounce) getDeviceScope(b broadcastable) []*remoteDevice {
	destination := b.getDestination()
	broadcastTargets := []*remoteDevice{}

	var target device
	err := bounce.database.First(&target, "id = ?", destination).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"scope":        b.getScope(),
				"destinations": destination,
				"type":         b.getType(),
			}).Error("device not found when determining broadcast scope for message")
			return broadcastTargets
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading device from database")
		}
	}
	rd := bounce.getRemoteDevice(target.Address)
	if rd.connectedSockets > 0 {
		broadcastTargets = append(broadcastTargets, rd)
	}

	return broadcastTargets
}

func (bounce *Bounce) getGroupScope(b broadcastable) []*remoteDevice {
	// TODO: look up the group, find all online devices
	return []*remoteDevice{}
}

//
// Send a message to any device that we're connected to right now
//
func (bounce *Bounce) getGlobalScope() []*remoteDevice {
	broadcastTargets := []*remoteDevice{}
	for _, dev := range bounce.devicePool.devices {
		if dev.connectedSockets > 0 {
			broadcastTargets = append(broadcastTargets, dev)
		}
	}
	return broadcastTargets
}
