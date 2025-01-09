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
var scopeCustom = 4

var typeDirectMessage = uint16(0)
var typeGroupMessage = uint16(1)
var typeReferenceOffer = uint16(2)
var typeReferenceRequest = uint16(3)
var typeCatchUp = uint16(4)
var typeAck = uint16(5)
var typeKeepAlive = uint16(6)
var typeSyncDeviceRequest = uint16(7)
var typeSyncDeviceRequestRejected = uint16(8)
var typeSyncDeviceRequestAccepted = uint16(9)
var typeDevice = uint16(10)
var typeUpdateDM = uint16(11)
var typeGroupCreation = uint16(12)
var typeUpdateGroup = uint16(13)
var typeTypingIndicator = uint16(14)
var typeAddUserRequest = uint16(15)
var typeAddUserRequestAccepted = uint16(16)
var typeAddUserRequestRejected = uint16(17)
var typeAddUser = uint16(18)
var typeConfirmation = uint16(19)
var typeUpdateUser = uint16(20)
var typeUpdateDevice = uint16(21)
var typeReadReceipt = uint16(22)
var typeUpdateSettings = uint16(23)

type sendable interface {
	getType() uint16
	getPayload() []byte
}

type broadcastable interface {
	sendable
	getID() uuid.UUID
	getScope(myID uuid.UUID) int
	getDestination(myID uuid.UUID) uuid.UUID
	getAuthor() uuid.UUID
	getTimestamp() int64
}

type sortableBroadcastables []broadcastable

func (sbrs sortableBroadcastables) Len() int {
	return len(sbrs)
}
func (sbrs sortableBroadcastables) Swap(i, j int) {
	sbrs[i], sbrs[j] = sbrs[j], sbrs[i]
}
func (sbrs sortableBroadcastables) Less(i, j int) bool {
	return sbrs[i].getTimestamp() < sbrs[j].getTimestamp()
}

func (b *bounce) getHandlers() map[uint16]func(string, []byte, bool) broadcastable {
	return map[uint16]func(string, []byte, bool) broadcastable{
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
		typeUpdateDM:                  b.handleUpdateDM,
		typeGroupCreation:             b.handleGroupCreation,
		typeUpdateGroup:               b.handleUpdateGroup,
		typeTypingIndicator:           b.handleTypingIndicator,
		typeAddUserRequest:            b.handleAddUserRequest,
		typeAddUserRequestAccepted:    b.handleAddUserRequestAccepted,
		typeAddUserRequestRejected:    b.handleAddUserRequestRejected,
		typeAddUser:                   b.handleAddUser,
		typeConfirmation:              b.handleConfirmation,
		typeUpdateUser:                b.handleUpdateUser,
		typeUpdateDevice:              b.handleUpdateDevice,
		typeReadReceipt:               b.handleReadReceipt,
		typeUpdateSettings:            b.handleUpdateSettings,
	}
}

func (b *bounce) broadcast(br broadcastable) {
	log.WithFields(log.Fields{
		"type":        br.getType(),
		"scope":       br.getScope(b.currentUserID()),
		"destination": br.getDestination(b.currentUserID()),
	}).Debug("broadcasting frame")
	for _, peer := range b.getBroadcastScope(br) {
		rd := b.getRemoteDevice(peer)
		if rd.connectedSockets > 0 {
			go func(dst chan sendable, msg broadcastable) {
				dst <- msg
			}(rd.messages, br)
		}
	}
}

func (b *bounce) sendDirect(peer string, br sendable) {
	rd := b.getRemoteDevice(peer)
	rd.messages <- br
}

func (b *bounce) getBroadcastScope(br broadcastable) []string {
	scope := br.getScope(b.currentUserID())

	if scope == scopeSync {
		return b.getSyncScope(br)
	} else if scope == scopeUser {
		return b.getUserScope(br)
	} else if scope == scopeGroup {
		return b.getGroupScope(br)
	} else if scope == scopeGlobal {
		return b.getGlobalScope(br)
	} else if scope == scopeCustom {
		return b.getCustomScope(br)
	} else {
		log.WithFields(log.Fields{
			"destination": br.getDestination(b.currentUserID()),
			"type":        br.getType(),
			"scope":       scope,
		}).Fatal("unknown broadcast scope")
	}

	return []string{}
}

func (b *bounce) getSyncScope(br broadcastable) []string {
	currentUser, exists := b.currentUser()
	if !exists {
		log.Fatal("cannot broadcast sync scoped frame when no current user exists")
	}

	broadcastTargets := []string{}
	for _, dev := range currentUser.Devices {
		if dev.RevokedAt != 0 {
			continue
		}
		if dev.Address == b.network.Address() {
			continue
		}
		if b.isDeliveredTo(br, dev.Address) {
			continue
		}
		broadcastTargets = append(broadcastTargets, dev.Address)
	}
	return broadcastTargets
}

func (b *bounce) getUserScope(br broadcastable) []string {
	broadcastTargets := []string{}

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
				"frame_id":    br.getID(),
				"scope":       br.getScope(b.currentUserID()),
				"destination": br.getDestination(b.currentUserID()),
				"type":        br.getType(),
			}).Error("user not found when determining broadcast scope for message")
			return broadcastTargets
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading user from database")
		}
	}
	for _, dev := range destinationUser.Devices {
		if dev.RevokedAt != 0 {
			continue
		}
		if b.isDeliveredTo(br, dev.Address) {
			continue
		}
		broadcastTargets = append(broadcastTargets, dev.Address)
	}

	// Add any sync devices that are online
	broadcastTargets = append(broadcastTargets, b.getSyncScope(br)...)

	return broadcastTargets
}

//
// Get any devices that we are connected to that belong to any members of a group, including ourself
//
func (b *bounce) getGroupScope(br broadcastable) []string {
	broadcastTargets := []string{}

	var destinationGroup group
	err := b.database.Preload("Users.Devices").Preload(clause.Associations).First(&destinationGroup, "id = ?", br.getDestination(b.currentUserID())).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"frame_id":    br.getID(),
				"type":        br.getType(),
				"destination": br.getDestination(b.currentUserID()),
			}).Debug("group not found when broadcasting group scoped message, using sync scope instead")
			return b.getSyncScope(br)
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error loading group from database")
		}
	}
	for _, u := range destinationGroup.Users {
		for _, dev := range u.Devices {
			if dev.RevokedAt != 0 {
				continue
			}
			if dev.Address == b.network.Address() {
				continue
			}
			if b.isDeliveredTo(br, dev.Address) {
				continue
			}
			broadcastTargets = append(broadcastTargets, dev.Address)
		}
	}

	return broadcastTargets
}

//
// Send a message to any device that we're connected to right now
//
func (b *bounce) getGlobalScope(br broadcastable) []string {
	broadcastTargets := []string{}

	author := br.getAuthor()
	if author == b.currentUserID() {
		allAddresses := []string{}
		err := b.database.Model(&device{}).Select("address").Where("revoked_at IS NULL OR revoked_at = 0").Find(&allAddresses).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting all device addresses")
		}
		// Anything global that we create can be sent to any known device
		for _, address := range allAddresses {
			if _, exists := b.getDeviceFromAddress(address); !exists {
				// Skip connections in the device pool if we don't have a device saved for them
				continue
			}
			if b.isDeliveredTo(br, address) {
				continue
			}
			broadcastTargets = append(broadcastTargets, address)
		}
	} else {
		// Anything global that was written by someone else should be sent to our devices, their devices,
		// and the devices of any users that have a group in common with the author
		var overlapDevices []device
		err := b.database.
			Distinct().
			Where(
				"(user_id = ? OR user_id = ? OR user_id IN (?))",
				b.currentUserID(),
				author,
				b.database.
					Model(&user{}).
					Distinct().
					Select("users.id").
					Joins("JOIN group_users ON group_users.user_id = users.id").
					Where(
						"group_users.group_id IN (?)",
						b.database.
							Model(&group{}).
							Distinct().
							Select("groups.id").
							Joins("JOIN group_users ON group_users.group_id = groups.id").
							Where("user_id = ?", author),
					),
			).
			Find(&overlapDevices).Error
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("error selecting unsent overlap devices during broadcast scoping")
		}
		for _, dev := range overlapDevices {
			if dev.RevokedAt != 0 {
				continue
			}
			if dev.Address == b.network.Address() {
				continue
			}
			if b.isDeliveredTo(br, dev.Address) {
				continue
			}
			broadcastTargets = append(broadcastTargets, dev.Address)
		}
	}

	return broadcastTargets
}

func (b *bounce) getCustomScope(br broadcastable) []string {
	broadcastTargets := []string{}

	var cs customScope
	err := b.database.Where("id = ?", br.getDestination(b.currentUserID())).First(&cs).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"frame_id": br.getID(),
				"type":     br.getType(),
				"id":       br.getID(),
				"scope":    br.getDestination(b.currentUserID()),
			}).Error("cannot broadcast to unknown custom scope")
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up custom scope")
		}
	}

	for _, addr := range cs.addresses() {
		if _, revoked := b.devicePool.revokedDevices[addr]; revoked {
			continue
		}
		if addr == b.network.Address() {
			continue
		}
		if b.isDeliveredTo(br, addr) {
			continue
		}
		broadcastTargets = append(broadcastTargets, addr)
	}

	return broadcastTargets
}
