package chat

import (
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const dialCooldown = time.Duration(5 * time.Minute) // TODO: should this be much larger?  Specific to the context?

type devicePool struct {
	deviceMutex   sync.Mutex
	devices       map[string]*remoteDevice
	lastDialMutex sync.Mutex
	lastDial      map[string]time.Time
}

func (dp *devicePool) getLastDial(address string) time.Time {
	dp.lastDialMutex.Lock()
	defer dp.lastDialMutex.Unlock()

	t, ok := dp.lastDial[address]
	if !ok {
		t = time.Time{}
		dp.lastDial[address] = t
	}
	return t
}

func (dp *devicePool) updateLastDial(address string) {
	dp.lastDialMutex.Lock()
	defer dp.lastDialMutex.Unlock()

	dp.lastDial[address] = time.Now()
}

func (b *bounce) peer() {
	b.auditPeers()
	ticker := time.NewTicker(30 * time.Second)
	for _ = range ticker.C {
		b.auditPeers()
	}
}

func (b *bounce) auditPeers() {
	// Send keep alive packets to each device that appears to have an open socket.  We do this reguardless of
	// if the network is online in order to detect dead sockets while the network is offline.
	go b.sendKeepAlives()

	// Skip this audit if the network isn't online
	if !b.networkIsOnline {
		return
	}

	// Always try to keep a socket open to every sync device
	go b.connectToSyncDevices()

	// Connect to any groups we have pending messages for or who we talk to frequently
	go b.connectToGroups()

	// Connect to any users we have pending messages for or who we talk to frequently
	go b.connectToUsers()
}

func (b *bounce) connectToSyncDevices() {
	currentUser, exists := b.currentUser()

	// If a profile hasn't been created on this device yet there's no device to sync with
	if !exists {
		return
	}

	for _, dev := range currentUser.Devices {
		if dev.Address == b.network.Address() {
			continue
		}
		rd := b.getRemoteDevice(dev.Address)
		if rd.connectedSockets == 0 {
			lastDial := b.devicePool.getLastDial(dev.Address)
			if time.Now().After(lastDial.Add(dialCooldown)) {
				go b.tryDialing(dev.Address)
			}
		}
	}
}

func (b *bounce) connectToGroups() {
	var allGroups []group
	err := b.database.Preload("Users.Devices").Preload(clause.Associations).Find(&allGroups).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading all groups")
	}

	// Connect to all groups // TODO: that we've talked to recently?
	for _, g := range allGroups {
		for _, u := range g.Users {
			// TODO: naive approach for now, ideally want to choose 4 random
			// users in the group to connect to
			b.userConnectionDesired(u.ID)
		}
	}
}

func (b *bounce) connectToUsers() {
	currentUser, exists := b.currentUser()

	if !exists {
		return
	}

	// First, ensure that we try to contact any user device we have messages for
	var allUsers []user
	err := b.database.Preload(clause.Associations).Where("profile = ?", false).Find(&allUsers).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading all non-profile users")
	}

	for _, u := range allUsers {
		for _, dev := range u.Devices {
			references := b.getReferenceOfferFor(dev.Address)
			if references.shouldDial() {
				lastDial := b.devicePool.getLastDial(dev.Address)
				if time.Now().After(lastDial.Add(dialCooldown)) {
					go b.tryDialing(dev.Address)
				}
			}
		}
	}

	// Connect to any users we have sent messages to in the last 3 days
	// TODO: unless we're under socket pressure?  in which case order by most recent
	// TODO: also what about people who have written us?  also connect to them
	var userIDs []uuid.UUID
	err = b.database.Model(&DirectMessage{}).
		Distinct("destination").
		Where(
			"source = ? and written_at > ?",
			currentUser.ID,
			time.Now().Add(-3*24*time.Hour).Unix(),
		).Find(&userIDs).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up users with recent direct messages")
		}
	}
	for _, id := range userIDs {
		if id != currentUser.ID {
			b.userConnectionDesired(id)
		}
	}

	// TODO; connect to all devices for users we added very recently
}

func (b *bounce) sendKeepAlives() {
	for address, rd := range b.devicePool.devices {
		if rd.connectedSockets > 0 {
			// Only send keep alives to connections from devices we have an identity for
			if _, ok := b.getDeviceFromAddress(address); ok {
				rd.messages <- keepAlive{}
			}
		}
	}
}

func (b *bounce) userConnectionDesired(id uuid.UUID) {
	if id == b.currentUserID() {
		// We always connect to sync devices, this is probably called because the UI
		// opened a thread to ourselves, we can ignore
		return
	}

	var u user
	err := b.database.Preload(clause.Associations).First(&u, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": id,
			}).Error("connection desired to unknown user")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user")
		}
	}
	for _, dev := range u.Devices {
		if b.getRemoteDevice(dev.Address).connectedSockets == 0 { // TODO: we want 2 open if we're actively chatting though
			lastDial := b.devicePool.getLastDial(dev.Address)
			if time.Now().After(lastDial.Add(dialCooldown)) {
				go b.tryDialing(dev.Address)
			}
		}
	}

}

func (b *bounce) groupConnectionDesired(id uuid.UUID) {
	// TODO: make sure we're peered with this group
}

func (b *bounce) tryDialing(address string) { // TODO: move cooldown logic in here?
	if !b.networkIsOnline {
		log.WithFields(log.Fields{
			"peer": address,
		}).Debug("ignoring request to dial while network is offline")
		return
	}

	b.devicePool.updateLastDial(address)
	log.WithFields(log.Fields{
		"peer": address,
	}).Debug("attempting to dial")
	conn, err := b.network.Dial(address)
	if err != nil {
		log.WithFields(log.Fields{
			"peer":  address,
			"error": err.Error(),
		}).Debug("error dialing")
	} else {
		log.WithFields(log.Fields{
			"peer": address,
		}).Debug("dialed")
		// TODO: callback to inform the UI that a user is online?  use a callback?
		b.insertConnectionIntoDevicePool(conn)
	}
}

func (b *bounce) getOnlinePeerAddresses(userID uuid.UUID) ([]string, error) {
	addresses := []string{}

	var u user
	err := b.database.Preload(clause.Associations).First(&u, "id = ?", userID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Error("user not found while looking for online devices")
			return addresses, err
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user")
		}
	}
	for _, dev := range u.Devices {
		if b.getRemoteDevice(dev.Address).connectedSockets > 0 { // TODO: we want 2 open if we're actively chatting though
			addresses = append(addresses, dev.Address)
		}
	}

	return addresses, nil
}
