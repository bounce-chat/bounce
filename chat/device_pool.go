package chat

import (
	"errors"
	"net"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
)

const dialCooldown = time.Duration(5 * time.Minute)

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

func (dp *devicePool) setLastDial(address string, t time.Time) {
	dp.lastDialMutex.Lock()
	defer dp.lastDialMutex.Unlock()

	dp.lastDial[address] = t
}

func (bounce *Bounce) peer() {
	if bounce.devicePool != nil {
		log.Fatal("attempted to start device pool after it has already been started")
	}

	bounce.devicePool = &devicePool{
		devices:      make(map[string]*remoteDevice),
		receivedAcks: make(map[string]bool),
		lastDial:     make(map[string]time.Time),
	}

	var allDevices []device
	err := bounce.database.Find(&allDevices).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading all devices from the database")
	}

	for _, dev := range allDevices { // TODO: is there really a reason to build these all upfront?
		if dev.Address == bounce.currentDevice().Address {
			continue
		}
		bounce.devicePool.devices[dev.Address] = newRemoteDevice()
	}

	go bounce.maintainPeers()
}

func (bounce *Bounce) maintainPeers() {
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

	// always try to maintain connection to groups that are recently communicated with
	// always try to maintain connection to users that are recently communicated with
	// dial anyone we've got pending messages for (users or groups where 0 devices have gotten a message, perhaps part of calculating the above two)
	// connect to anyone asked by the UI

	// Send keep alive packets to each device that appears to have an open socket
	go bounce.sendKeepAlives()

	// TODO: just for now, let's dial every device we know about if it doesn't have a connection
	for address, rd := range bounce.devicePool.devices {
		if address != bounce.currentDevice().Address && rd.connectedSockets == 0 {
			go bounce.tryDialing(address)
		}
	}
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

func (bounce *Bounce) sendKeepAlives() {
	for _, rd := range bounce.devicePool.devices {
		if rd.connectedSockets != 0 {
			//rd.messages <-keepAlive{} // TODO: build this broadcastable type
		}
	}
}

func (bounce *Bounce) tryDialing(address string) {
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

	references, needed := bounce.getReferenceOfferFor(peerAddress)
	if needed {
		go bounce.broadcastReferenceOffer(references)
	}
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
			references, needed := bounce.getReferenceOfferFor(conn.RemoteAddr().String())
			if needed {
				go bounce.broadcastReferenceOffer(references)
			}
			return
		}
	}
	// TODO: channel is closed, shutting down bounce
	rd.connectedSockets -= 1
	conn.Close()
}
