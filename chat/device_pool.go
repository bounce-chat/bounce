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

	if scope == USER_SCOPE {
		var destinationUser user
		result := bounce.database.Model(&user{}).Preload(clause.Associations).Find(&destinationUser, destination)
		if result.Error != nil {
			return broadcastTargets, result.Error
		}
		if result.RowsAffected == 0 {
			return broadcastTargets, errors.New("no devices found belonging to destination user")
		}
		for _, dev := range destinationUser.Devices {
			// TODO: skip if it's already been delivered to this device.  Need to know a broadcastable's PK and be able to query already delivered devices
			// polymorphic association to broadcastable metadata?
			rd := bounce.getRemoteDevice(dev.Address)
			if rd.connectedSockets > 0 {
				broadcastTargets = append(broadcastTargets, rd)
			}
		}
		// TODO: make sure to always add sync devices
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

	writeReferences := false
	if rd.connectedSockets == 0 {
		writeReferences = true
	}

	go bounce.readFrames(conn)
	go bounce.writeFrames(rd, conn)

	if writeReferences {
		// Select all things that should be sent to this device but haven't been yet (by us)
		// write those references
	}
}

func (bounce *Bounce) writeFrames(rd *remoteDevice, conn net.Conn) {
	rd.connectedSockets += 1
	rd.closer.Add(1)
	defer rd.closer.Done()

	for b := range rd.messages {
		err := writeFrame(conn, b.getType(), b.getPayload())
		if err == nil {
			// TODO: messages can be dropped and still not return an error!  How to handle this?
			// perhaps there's a type of reference acks that the peer sends back and we update delivery
			// status and UI indicators then?
			b.deliveredTo(conn.RemoteAddr().String()) // TODO: pass the database.  pass the UI?
			// TODO: UI callbacks for delivery status
		} else {
			rd.connectedSockets -= 1
			// TODO: if this was the last alive socket, drain the channel?  Or maybe re-write to the channel?
			// TODO: we should also test all the other sockets at this point.  Perhaps just write enough health
			// checks into the channel so that each socket will try to send one?
			return
		}
	}
	// TODO: channel is closed, shutting down bounce
	rd.connectedSockets -= 1
	conn.Close()
}
