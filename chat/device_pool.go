package chat

import (
	"errors"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const dialCooldown = time.Duration(5 * time.Minute) // TODO: should this be much larger?  Specific to the context?

type devicePool struct {
	deviceMutex       sync.Mutex
	devices           map[string]*remoteDevice
	receivedAcksMutex sync.Mutex
	receivedAcks      map[string]bool
	lastDialMutex     sync.Mutex
	lastDial          map[string]time.Time
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

func (bounce *Bounce) peer() {
	// TODO: figure out the right way to close this down during shutdown
	bounce.auditPeers()
	ticker := time.NewTicker(30 * time.Second)
	for _ = range ticker.C {
		bounce.auditPeers()
	}
}

func (bounce *Bounce) auditPeers() {
	// Always try to keep a socket open to every sync device
	go bounce.connectToSyncDevices()

	// Connect to any groups we have pending messages for or who we talk to frequently
	go bounce.connectToGroups()

	// Connect to any users we have pending messages for or who we talk to frequently
	go bounce.connectToUsers()

	// Send keep alive packets to each device that appears to have an open socket
	go bounce.sendKeepAlives()
}

func (bounce *Bounce) connectToSyncDevices() {
	for _, dev := range bounce.currentUser().Devices {
		if dev.Address == bounce.currentDevice().Address {
			continue
		}
		rd := bounce.getRemoteDevice(dev.Address)
		if rd.connectedSockets == 0 {
			lastDial := bounce.devicePool.getLastDial(dev.Address)
			if time.Now().After(lastDial.Add(dialCooldown)) {
				go bounce.tryDialing(dev.Address)
			}
		}
	}
}

func (bounce *Bounce) connectToGroups() {
	// TODO
}

func (bounce *Bounce) connectToUsers() {
	// First, ensure that we try to contact any user device we have messages for
	var allUsers []user
	err := bounce.database.Preload(clause.Associations).Where("profile = ?", false).Find(&allUsers).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading all non-profile users")
	}

	for _, u := range allUsers {
		for _, dev := range u.Devices {
			references := bounce.getReferenceOfferFor(dev.Address)
			if references.hasContent() { // TODO: this should actually check for anything except group messages (if it's only group messages, we don't need to try to dial?)
				go bounce.tryDialing(dev.Address)
			}
		}
	}

	// Connect to any users we have sent messages to in the last 3 days
	// TODO: unless we're under socket pressure?  in which case order by most recent
	var userIDs []uuid.UUID
	err = bounce.database.Model(&DirectMessage{}).
		Distinct("destination").
		Where(
			"source = ? and created_at > ?",
			bounce.currentUser().ID,
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
		bounce.userConnectionDesired(id)
	}
}

func (bounce *Bounce) sendKeepAlives() {
	for address, rd := range bounce.devicePool.devices {
		if rd.connectedSockets > 0 {
			dev, ok := bounce.getDeviceFromAddress(address)
			if ok {
				rd.messages <- keepAlive{destination: dev.ID}
			}
		}
	}
}

func (bounce *Bounce) userConnectionDesired(id uuid.UUID) {
	var u user
	err := bounce.database.Preload(clause.Associations).First(&u, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": id,
			}).Error("user not found for direct message")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user")
		}
	}
	for _, dev := range u.Devices {
		if bounce.getRemoteDevice(dev.Address).connectedSockets == 0 { // TODO: we want 2 open if we're actively chatting though
			lastDial := bounce.devicePool.getLastDial(dev.Address)
			if time.Now().After(lastDial.Add(dialCooldown)) {
				go bounce.tryDialing(dev.Address)
			}
		}
	}

}

func (bounce *Bounce) tryDialing(address string) {
	bounce.devicePool.updateLastDial(address)
	log.WithFields(log.Fields{
		"peer": address,
	}).Info("attempting to dial")
	conn, err := bounce.network.Dial(address)
	if err != nil {
		log.WithFields(log.Fields{
			"peer":  address,
			"error": err.Error(),
		}).Error("error dialing")
	} else {
		log.WithFields(log.Fields{
			"peer": address,
		}).Info("dialed")
		// TODO: callback to inform the UI that a user is online?  use a callback?
		bounce.insertConnectionIntoDevicePool(conn)
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

type remoteDevice struct {
	connectedSockets int
	messages         chan broadcastable
	closer           sync.WaitGroup
}

func newRemoteDevice() *remoteDevice {
	return &remoteDevice{
		connectedSockets: 0,
		messages:         make(chan broadcastable),
	}
}

func (bounce *Bounce) getRemoteDevice(address string) *remoteDevice {
	bounce.devicePool.deviceMutex.Lock()
	defer bounce.devicePool.deviceMutex.Unlock()

	rd, ok := bounce.devicePool.devices[address]
	if !ok {
		rd = newRemoteDevice()
		bounce.devicePool.devices[address] = rd
	}
	return rd
}

func (bounce *Bounce) insertConnectionIntoDevicePool(conn net.Conn) {
	peerAddress := conn.RemoteAddr().String()
	rd := bounce.getRemoteDevice(peerAddress)

	go bounce.readFrames(conn)
	go bounce.writeFrames(rd, conn)

	bounce.sendReferences(peerAddress)
}

func (bounce *Bounce) writeFrames(rd *remoteDevice, conn net.Conn) {
	rd.connectedSockets += 1
	rd.closer.Add(1)
	defer rd.closer.Done()

	for b := range rd.messages {
		err := writeFrame(conn, b.getType(), b.getPayload())
		if err != nil {
			rd.connectedSockets -= 1
			// TODO: if we now have 0 connections, let the UI know the user is offline
			bounce.sendReferences(conn.RemoteAddr().String())
			return
		}
	}
	// TODO: channel is closed, shutting down bounce
	rd.connectedSockets -= 1
	conn.Close()
}
