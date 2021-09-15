package chat

import (
	"errors"
	"net"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
)

type devicePool struct {
	sync.Mutex
	devices map[string]*remoteDevice
}

func (bounce *Bounce) peer() {
	if bounce.devicePool != nil {
		log.Fatal("attempted to start device pool after it has already been started")
	}

	bounce.devicePool = &devicePool{
		devices: make(map[string]*remoteDevice),
	}

	var allDevices []device
	err := bounce.database.Find(&allDevices).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading all devices from the database")
	}

	for _, dev := range allDevices {
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
	// TODO: just for now, let's dial every device we know about if it doesn't have a connection
	for address, rd := range bounce.devicePool.devices {
		if address != bounce.currentDevice().Address && rd.connectedSockets == 0 {
			go bounce.tryDialing(address)
		}
	}
	// always try to dial sync devices
	// always try to maintain connection to groups that aren't dead
	// always try to maintain connection to users that are recently communicated with
	// dial anyone we've got pending messages for (in the database, or if the len of the messages channel >0 while the number of sockets is 0?)
	// connect to anyone asked by the UI
	// send keep-alive tests to each connected device

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
	lastError        int64
}

func newRemoteDevice() *remoteDevice {
	return &remoteDevice{
		connectedSockets: 0,
		messages:         make(chan broadcastable),
	}
}

func (bounce *Bounce) getRemoteDevice(address string) *remoteDevice {
	bounce.devicePool.Lock()
	defer bounce.devicePool.Unlock()

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
		go bounce.broadcast(references)
	}

	/*
		// Before writing any other frames down this socket, first deliver the reference offer.
		// This is because we might have other sockets for this device that just died and
		// we haven't detected that yet, we don't want the offer to get lost in one of those
		// sockets.  TODO: rather than doing this, since this is going to be an issue all over the place,
		// either have one dead socket reset all other sockets as well, or run references on a loop
		//
		// could have each reader store it's start time, and every time there's an error we kill all
		// readers that were created before the error?
		references, needed := bounce.getReferenceOfferFor(peerAddress)
		if needed {
			err := writeFrame(conn, references.getType(), references.getPayload())
			if err != nil {
				return
			}
		}

		go bounce.writeFrames(rd, conn)
	*/

}

func (bounce *Bounce) writeFrames(rd *remoteDevice, conn net.Conn) {
	createdAt := time.Now().Unix()
	rd.connectedSockets += 1
	rd.closer.Add(1)
	defer rd.closer.Done()

	for b := range rd.messages {
		if rd.lastError > createdAt {
			// Another socket died after this one was created, and this one might be dead too.
			// Write doesn't return an error until the socket has been dead for some time, so
			// we're just going to assume this one is dead and let the newer sockets transport.
			rd.messages <- b
			rd.connectedSockets -= 1
			return
		}
		err := writeFrame(conn, b.getType(), b.getPayload())
		if err != nil {
			rd.connectedSockets -= 1
			rd.lastError = time.Now().Unix()
			// TODO: automatically retry here?
			// rd.messages <- b
			return
		}
	}
	// TODO: channel is closed, shutting down bounce
	rd.connectedSockets -= 1
	conn.Close()
}
