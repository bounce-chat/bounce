package chat

import (
	"errors"
	"net"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type devicePool struct {
	devices map[string]*remoteDevice // TODO: maybe replace this struct with just this top level map
}

func (bounce *Bounce) startDevicePool() {
	devicePool := &devicePool{
		devices: make(map[string]*remoteDevice),
	}

	var allDevices []device
	err := bounce.database.Find(&allDevices).Error
	if err != nil {
		// TODO: err log
	}

	for _, dev := range allDevices {
		// TODO: exclude the current device
		devicePool.devices[dev.Address] = &remoteDevice{
			connectedSockets: 0,
			messages:         make(chan broadcastable),
		}
	}

	go bounce.maintainDevicePoolConnections()

	bounce.devicePool = devicePool
}

func (bounce *Bounce) maintainDevicePoolConnections() { // go bounce.peer() after network online for the firs time?
	// forever loop
	ticker := time.NewTicker(30 * time.Second) // TODO: doesn't tick immediately.  I want it to.
	for _ = range ticker.C {
		// TODO: just for now, let's dial every device we know about if it doesn't have a connection
		for address, rd := range bounce.devicePool.devices {
			if address != bounce.currentDevice().Address && rd.connectedSockets == 0 {
				go bounce.tryDialing(address)
			}
		}
	}
	// always try to dial sync devices
	// always try to maintain connection to groups that aren't dead
	// always try to maintain connection to users that are recently communicated with
	// dial anyone we've got pending messages for (in the database, or if the len of the messages channel >0 while the number of sockets is 0?)
	// connect to anyone asked by the UI
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

func (bounce *Bounce) getBroadcastScope(b broadcastable) ([]*remoteDevice, error) { // TODO: move to protocol?
	scope := b.getScope()
	destination := b.getDestination()
	broadcastTargets := []*remoteDevice{}

	if scope == USER_SCOPE {
		var destinationUser user
		result := bounce.database.Find(&destinationUser, destination)
		if result.Error != nil {
			return broadcastTargets, result.Error
		}
		if result.RowsAffected == 0 {
			return broadcastTargets, errors.New("no devices found belonging to destination user")
		}
		for _, dev := range destinationUser.Devices {
			// TODO: skip if it's already been delivered to this device.  Need to know a broadcastable's PK and be able to query already delivered devices
			// polymorphic association to broadcastable metadata?
			rd, ok := bounce.devicePool.devices[dev.Address]
			if !ok {
				// TODO: Hasn't been loaded before, create it?
			}
			if rd.connectedSockets > 0 {
				broadcastTargets = append(broadcastTargets, rd)
			}
		}
		// TODO: make sure to add sync devices
	}

	// TODO: err if no devices are online?

	return broadcastTargets, nil
}

type remoteDevice struct {
	connectedSockets int
	messages         chan broadcastable
	closer           sync.WaitGroup
}

func (bounce *Bounce) insertConnectionIntoDevicePool(conn net.Conn) {
	peerAddress := conn.RemoteAddr().String()
	rd, ok := bounce.devicePool.devices[peerAddress]
	if !ok {
		rd = &remoteDevice{
			connectedSockets: 0,
			messages:         make(chan broadcastable),
		}
		bounce.devicePool.devices[peerAddress] = rd
	}

	// write references to missing messages?
	go bounce.readFrames(conn)
	go bounce.writeFrames(rd, conn)

}

func (bounce *Bounce) writeFrames(rd *remoteDevice, conn net.Conn) {
	rd.connectedSockets += 1
	rd.closer.Add(1)
	defer rd.closer.Done()

	log.Info("ready to write frames that get sent to the channel")

	for b := range rd.messages {
		err := writeFrame(conn, b.getType(), b.getPayload())
		if err == nil {
			log.Info("write a frame")
			b.deliveredTo(conn.RemoteAddr().String()) // TODO: pass the database.  pass the UI?
			// TODO: UI callbacks for delivery status
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error writing a frame")
			rd.connectedSockets -= 1
			// TODO: if this was the last alive socket, drain the channel?
			return
		}
	}
	// TODO: channel is closed, shutting down bounce
	rd.connectedSockets -= 1
	conn.Close()
}
