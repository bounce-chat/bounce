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

func (b *bounce) peer() {
	// TODO: figure out the right way to close this down during shutdown
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
	// TODO
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
}

func (b *bounce) sendKeepAlives() {
	for address, rd := range b.devicePool.devices {
		if rd.connectedSockets > 0 {
			dev, ok := b.getDeviceFromAddress(address)
			if ok {
				rd.messages <- keepAlive{destination: dev.ID}
			}
		}
	}
}

func (b *bounce) userConnectionDesired(id uuid.UUID) {
	var u user
	err := b.database.Preload(clause.Associations).First(&u, "id = ?", id).Error
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
		if b.getRemoteDevice(dev.Address).connectedSockets == 0 { // TODO: we want 2 open if we're actively chatting though
			lastDial := b.devicePool.getLastDial(dev.Address)
			if time.Now().After(lastDial.Add(dialCooldown)) {
				go b.tryDialing(dev.Address)
			}
		}
	}

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

func (b *bounce) getRemoteDevice(address string) *remoteDevice {
	b.devicePool.deviceMutex.Lock()
	defer b.devicePool.deviceMutex.Unlock()

	rd, ok := b.devicePool.devices[address]
	if !ok {
		rd = newRemoteDevice()
		b.devicePool.devices[address] = rd
	}
	return rd
}

func (b *bounce) insertConnectionIntoDevicePool(conn net.Conn) {
	peerAddress := conn.RemoteAddr().String()
	rd := b.getRemoteDevice(peerAddress)

	go b.readFrames(conn)
	go b.writeFrames(rd, conn)

	b.sendReferences(peerAddress)
}

func (b *bounce) readFrames(conn net.Conn) { // TODO: move to protocol or something else?
	handlers := b.getHandlers()
	peer := conn.RemoteAddr().String()
	// Get the peer address
	// reject it if it isn't a known device?  Maybe don't want to if introductions / group membership is out of order
	// If it isn't know perhaps we put it in some limited handshake flow for new devices
	for {
		frameType, data, err := readFrame(conn) // TODO: just read the header first, make sure we want to read the rest in the context of the device (untrusted devices can't send large messages, etc)
		if err != nil {
			return
		}
		// TODO: some type of filtering on which types of peers can send which types of messages
		handler, ok := handlers[frameType]
		if !ok {
			log.WithFields(log.Fields{
				"peer": peer,
				"type": frameType,
			}).Error("peer sent an unsupported frame type, disconnecting")
			conn.Close()
			return
		} else {
			go handler(peer, data)
		}
	}
}

func (b *bounce) writeFrames(rd *remoteDevice, conn net.Conn) {
	rd.connectedSockets += 1
	rd.closer.Add(1)
	defer rd.closer.Done()

	for br := range rd.messages {
		err := writeFrame(conn, br.getType(), br.getPayload())
		if err != nil {
			rd.connectedSockets -= 1
			// TODO: if we now have 0 connections, let the UI know the user is offline
			b.sendReferences(conn.RemoteAddr().String())
			return
		}
	}
	// TODO: channel is closed, shutting down bounce
	rd.connectedSockets -= 1
	conn.Close()
}
