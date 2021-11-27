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

type broadcastable interface {
	getScope() int
	getDestination() uuid.UUID // A group or user ID depending on the scope
	getType() uint16
	getPayload() []byte
	isAlreadyDeliveredTo(address string) bool
}

func (b *bounce) getHandlers() map[uint16]func(string, []byte) {
	return map[uint16]func(string, []byte){
		typeDirectMessage:    b.handleDirectMessage,
		typeReferenceOffer:   b.handleReferenceOffer,
		typeReferenceRequest: b.handleReferenceRequest,
		typeCatchUp:          b.handleCatchUp,
		typeAck:              b.handleAck,
		typeKeepAlive:        b.handleKeepAlive,
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
		return b.getSyncScope()
	} else if scope == scopeUser {
		return b.getUserScope(br)
	} else if scope == scopeDevice {
		return b.getDeviceScope(br)
	} else if scope == scopeGroup {
		return b.getGroupScope(br)
	} else if scope == scopeGlobal {
		return b.getGlobalScope()
	} else {
		log.WithFields(log.Fields{
			"destination": br.getDestination(),
			"type":        br.getType(),
			"scope":       scope,
		}).Fatal("unknown broadcast scope")
	}

	return []*remoteDevice{}
}

func (b *bounce) getSyncScope() []*remoteDevice {
	currentUser, exists := b.currentUser()

	if !exists {
		// TODO: fatal?
	}

	broadcastTargets := []*remoteDevice{}
	for _, dev := range currentUser.Devices {
		if dev.Address == b.network.Address() {
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

	// Add any devices that are owned by the destination user that are online
	var destinationUser user
	err := b.database.Preload(clause.Associations).First(&destinationUser, "id = ?", br.getDestination()).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"scope":        br.getScope(),
				"destinations": br.getDestination(),
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
	broadcastTargets = append(broadcastTargets, b.getSyncScope()...)

	return broadcastTargets
}

func (b *bounce) getDeviceScope(br broadcastable) []*remoteDevice {
	destination := br.getDestination()
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
func (b *bounce) getGlobalScope() []*remoteDevice {
	broadcastTargets := []*remoteDevice{}
	for _, dev := range b.devicePool.devices {
		if dev.connectedSockets > 0 {
			broadcastTargets = append(broadcastTargets, dev)
		}
	}
	return broadcastTargets
}
