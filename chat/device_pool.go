package chat

import (
	"errors"
	"net"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
)

type devicePool struct {
	groups  map[uuid.UUID]*connectionGroup
	users   map[uuid.UUID]*connectionGroup
	sync    *connectionGroup
	devices map[uuid.UUID]*remoteDevice
}

func (bounce *Bounce) startDevicePool() {
	devicePool := &devicePool{
		groups:  make(map[uuid.UUID]*connectionGroup),
		users:   make(map[uuid.UUID]*connectionGroup),
		devices: make(map[uuid.UUID]*remoteDevice),
	}

	// Create a connection group for the sync devices, devices owned by the same user as this instance
	var myProfile user
	err := bounce.database.Model(&user{}).Preload(clause.Associations).Where("profile = ?", true).First(&myProfile).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading user profile")
	}
	syncDevices := &connectionGroup{
		connectionDesired: true,
	}
	for _, dev := range myProfile.Devices {
		if dev.ID == bounce.currentDevice().ID {
			continue
		} // TODO: can't know current device address until the network starts up.
		newDevice, ok := devicePool.devices[dev.ID]
		if !ok {
			newDevice = newRemoteDevice(dev)
			devicePool.devices[dev.ID] = newDevice
		}
		syncDevices.offline = append(syncDevices.offline, newDevice)
	}
	devicePool.sync = syncDevices
	go devicePool.sync.maintainConnection(bounce.network)

	// Create a connection group for each user
	var allUsers []user
	err = bounce.database.Model(&user{}).Preload(clause.Associations).Where("profile = ?", false).Find(&allUsers).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading all users")
	}
	for _, u := range allUsers {
		// TODO: replace al lthis with a call to something like dialUser except with an argument on if it should be connected to?
		userDevices := &connectionGroup{
			connectionDesired: false, // Should be true if there are pending messages
		}
		for _, dev := range u.Devices {
			newDevice, ok := devicePool.devices[dev.ID]
			if !ok {
				newDevice = newRemoteDevice(dev)
				devicePool.devices[dev.ID] = newDevice
			}
			userDevices.offline = append(userDevices.offline, newDevice)
		}
		devicePool.users[u.ID] = userDevices
		// TODO: probably doesn't make sense to flush pending messages in the db here, but somewhere after devicePool creation we need
		// to load everything that's pending and write it to the network as a reference.  This is probably best done as queries on the
		// users and groups.  Do this in some callback on Accept and Dial.

		// TODO: just conecting to all users for testing right now.  In the future be smart about who needs to be dialed
		go userDevices.maintainConnection(bounce.network) // TODO: do I want to need to pass network here?
	}

	// TODO: create connection groups for all all groups.  Waiting on a database concept for groups

	bounce.devicePool = devicePool
}

func (bounce *Bounce) insertIntoDevicePool(conn net.Conn) {
	address := conn.RemoteAddr().String()
	var dev device
	found := bounce.database.Model(&device{}).Where("address = ?", address).First(&dev).RowsAffected
	if found == 0 {
		// We've accepted a connection from a device we're not aware of.  This may be a user importing
		// our contact for the first time.  TODO: if we go ahead with the import, we're going to want
		// to attach this connection to that user after.  Need to figure out how to do that.
		return
	}

	rd, ok := bounce.devicePool.devices[dev.ID]
	if !ok {
		rd = newRemoteDevice(dev)
		bounce.devicePool.devices[dev.ID] = rd
		// We aren't aware of this device in the device pool yet.
		// TODO: add it to the right user?  look up the user UUID, add it to the online slice in that user's connection pool
		// TODO: for groups, look up the groups that user is in and add it to the online devices in those groups' connection pools
	}

	//
	// Have an equal number of connections that are used for small messages as for large messages, preferring
	// to add new small ones first, until a maximum of 5 large connections are open, at which point all new
	// connections are designated for small frames.  It is unexpected to ever have that many connections open
	// to the same device however.
	//
	smallChannelCount := rd.onlineSmallChannels()
	largeChannelCount := rd.onlineLargeChannels()
	if smallChannelCount > largeChannelCount {
		if largeChannelCount < 5 {
			rd.largeFrames = append(rd.largeFrames, &remoteConnection{connection: conn, alive: true})
		} else {
			rd.smallFrames = append(rd.smallFrames, &remoteConnection{connection: conn, alive: true})
		}
	} else {
		rd.smallFrames = append(rd.smallFrames, &remoteConnection{connection: conn, alive: true})
	}
}

//func (bounce *Bounce) removeFromDevicePool(conn net.Conn) {
//
//}

/*
func (dp *devicePool) isUserOnline(id uuid.UUID) bool {
	// TODO: is thig needed?  check all the devices, look for an open socket
	return false
}
*/

//
// A conection group is a collection of devices that represent one gossip scope, such as a chat group, a remote user, or your devices
//
type connectionGroup struct {
	connectionDesired bool            // TODO: this state transition should be done with function calls?
	connected         []*remoteDevice // TODO: manage the transition between these two slices.  Maybe they should be maps from addresses to make it easier?
	offline           []*remoteDevice
}

// TODO: stay connected to a user/group/sync
func (bounce *Bounce) maintainUserConnection(id uuid.UUID) {
}
func (bounce *Bounce) maintainGroupConnection(id uuid.UUID) {
}

func (bounce *Bounce) maintainConnection(cg *connectionGroup) {
}

func (cg *connectionGroup) maintainConnection(network BounceNetwork) {
	// TODO: dial the correct number of devices, monitor them and keep this group integrated
	// if we're no longer "connectionDesired", stop doing that, but monitor for when we want it again?  or maybe just wait for another call?
	// TODO: connect to log of the total number of devices, with a min of 15?

	// TODO: just for testing, dial everything
	for _, offlineDevice := range cg.offline {
		err := offlineDevice.dial(network)
		if err == nil {
			cg.connected = append(cg.connected, offlineDevice)
		}
	}
}

// TODO: Accept() should create one of these and insert it into the pool, then read frames
type remoteDevice struct {
	// TODO: lock this for manipulating the remote connections?
	device      device
	smallFrames []*remoteConnection
	largeFrames []*remoteConnection // TODO: usage determined by len(pendingFrame.payload)
	// TODO: if there's "socket pressue" (tons of open connections), maybe only keep one socket open
}

func newRemoteDevice(device device) *remoteDevice {
	rd := &remoteDevice{
		device: device,
	}

	return rd
}

//
// Dial a connection to the device specified by UUID
//
func (bounce *Bounce) dialDevice(id uuid.UUID) error {
	rd, ok := bounce.devicePool.devices[id]
	if !ok {
		// Attmpeting to dial a device we don't know about yet.  Add it?
		// if we're going to do that we need to pass the whole device in
		// Also, we'll want to associate it with the proper user/groups in the
		// pool so that information will need to be gathered / passed here as well
	}

	connection, err := bounce.network.Dial(rd.device.Address)
	if err != nil {
		// TODO: debug logging for now
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"address": rd.device.Address,
		}).Error("error dialing device")
		return err
	}
	// TODO: debug logging for now
	log.WithFields(log.Fields{
		"address": rd.device.Address,
	}).Info("dialed device")

	bounce.insertIntoDevicePool(connection)
	go bounce.readFrames(connection)

	return nil
}

func (rd *remoteDevice) dial(network BounceNetwork) error {
	log.WithFields(log.Fields{
		"address": rd.device.Address,
	}).Info("attempting to dial device")

	connection, err := network.Dial(rd.device.Address)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"address": rd.device.Address,
		}).Error("error dialing device")
		return err
	} else {
		log.WithFields(log.Fields{
			"address": rd.device.Address,
		}).Info("dialed device")
		rd.smallFrames = append(rd.smallFrames, &remoteConnection{connection: connection, alive: true})
		// TODO: start reading frames from it
		//bounce.readFrames(connection)
	}
	return nil
}

func (rd *remoteDevice) onlineSmallChannels() int {
	onlineSmallChannels := 0
	for _, connection := range rd.smallFrames {
		if connection.alive {
			onlineSmallChannels += 1
		}
	}
	return onlineSmallChannels
}

func (rd *remoteDevice) onlineLargeChannels() int {
	onlineLargeChannels := 0
	for _, connection := range rd.smallFrames {
		if connection.alive {
			onlineLargeChannels += 1
		}
	}
	return onlineLargeChannels
}

func (rd *remoteDevice) writeFrame(frameType uint16, payload []byte) error { // TODO: some writeLowPriroityFrame for references in highly-connected connection group?
	small := len(payload) < 1024

	onlineSmallChannels := 0
	for _, connection := range rd.smallFrames {
		if connection.alive {
			onlineSmallChannels += 1
		}
	}

	onlineLargeChannels := 0
	for _, connection := range rd.smallFrames {
		if connection.alive {
			onlineLargeChannels += 1
		}
	}

	if onlineSmallChannels+onlineLargeChannels == 0 {
		return errors.New("no available connections to device")
	}

	// TODO: just writing down index 0 for now, fix this with a round robin.  Also handle failues
	if small {
		if onlineSmallChannels > 0 {
			return rd.smallFrames[0].writeFrame(frameType, payload)
		} else {
			return rd.largeFrames[0].writeFrame(frameType, payload)
		}
	} else {
		if onlineLargeChannels > 0 {
			return rd.largeFrames[0].writeFrame(frameType, payload)
		} else {
			return rd.smallFrames[0].writeFrame(frameType, payload)
		}
	}

	// if some fail, properly dispose of them and try other sockets while kicking off other dials.
	// if this device is totally offline however, some way to communicate that up the connection
	// group is needed
	return nil
}

/*
func (rd *remoteDevice) insert(connection net.Conn) {
	if len(rd.smallFrames) == 0 {
		rd.smallFrames = append(rd.smallFrames, &remoteConnection{connection: connection, alive: true})
	}
}
*/

type remoteConnection struct {
	sync.Mutex
	alive      bool
	busy       bool
	connection net.Conn
}

func (rc *remoteConnection) writeFrame(frameType uint16, payload []byte) error {
	rc.Lock()
	defer rc.Unlock()

	rc.busy = true
	err := writeFrame(rc.connection, frameType, payload)
	if err != nil {
		rc.alive = false
	}
	rc.busy = false
	return err
}

/*
func (bounce *Bounce) dialUser(u *user) {
	// If we don't have a connection to this user already maintained, create one
	// TODO: how to handle new devices added to a group?  just connect when we learn about them?
	group, exists := bounce.devicePool.users[u.ID]
	if exists {
		group.connectionDesired = true
		// TODO: redial if needed
	} else {
		// TODO: error?  This should user should have been created at startup, or when it was introduced on the wire.  Maybe this is actually what should
		// be called when a user is introduced over the wire.
		group = &connectionGroup{
			connectionDesired: true,
		}
		bounce.devicePool.users[u.ID] = group
		for _, device := range u.Devices { // TODO: random selection if group size is too large?  database LIMIT query?
			userDevice := newRemoteDevice(device)
			group.offline = append(group.offline, userDevice)
			go group.maintainConnection(bounce.network)
		}
	}
}
*/
