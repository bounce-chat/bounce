package chat

import (
	"errors"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
)

var SYNC_SCOPE = 0 // TODO: unexport these
var USER_SCOPE = 1
var GROUP_SCOPE = 2

//var GLOBAL_SCOPE = 3 // TODO: how should this be used?  all groups + all users not in groups?
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
	getType() uint16           // TODO: make these a custom type?
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
	peerScope, err := bounce.getBroadcastScope(b)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting broadcast targets")
		// TODO: don't need to error if there's just noone online  maybe handle other error logs in the get functions
		return
	}

	for _, peer := range peerScope {
		// Async try to write this message to every device that should be written to
		go func(dst chan broadcastable, msg broadcastable) {
			dst <- msg
		}(peer.messages, b)
	}
}

func (bounce *Bounce) getBroadcastScope(b broadcastable) ([]*remoteDevice, error) {
	scope := b.getScope()
	destination := b.getDestination()
	broadcastTargets := []*remoteDevice{}

	if scope == USER_SCOPE { // TODO: break these out
		var destinationUser user
		result := bounce.database.Model(&user{}).Preload(clause.Associations).Find(&destinationUser, destination)
		if result.Error != nil {
			return broadcastTargets, result.Error
		}
		if result.RowsAffected == 0 {
			return broadcastTargets, errors.New("no devices found belonging to destination user")
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
		// TODO: make sure to always add sync devices
	} else if scope == DEVICE_SCOPE {
		var target device
		result := bounce.database.First(&target, b.getDestination())
		if result.Error != nil {
			return broadcastTargets, result.Error
		}
		if result.RowsAffected == 0 {
			return broadcastTargets, errors.New("no device found in database for broadcastable message tageting device") // TODO: log the UUID
		}
		rd := bounce.getRemoteDevice(target.Address)
		if rd.connectedSockets > 0 {
			broadcastTargets = append(broadcastTargets, rd)
		}
	}

	// TODO: err if no devices are online?

	return broadcastTargets, nil
}
