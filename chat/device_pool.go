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

func (bounce *Bounce) newDevicePool() *devicePool {
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
		// TODO: exclude current device
		syncDevices.offline = append(syncDevices.offline, newRemoteDevice(dev))
	}
	devicePool.sync = syncDevices
	go devicePool.sync.maintainConnection()

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
			userDevices.offline = append(userDevices.offline, newRemoteDevice(dev)) // TODO: don't make a new one every time, use the lookup feature
		}
		devicePool.users[u.ID] = userDevices
		// TODO: probably doesn't make sense to flush pending messages in the db here, but somewhere after devicePool creation we need
		// to load everything that's pending and write it to the network as a reference.  This is probably best done as queries on the
		// users and groups
	}

	// TODO: create connection groups for all all groups.  Waiting on a database concept for groups

	return devicePool
}

/*
func (dp *devicePool) insert(connections net.Conn) { // TODO: or: bounce.insertIntoDevicePool(conn)

}

func (dp *devicePool) remove(connections net.Conn) {

}

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

func (cg *connectionGroup) maintainConnection() {
	// TODO: dial the correct number of devices, monitor them and keep this group integrated
	// if we're no longer "connectionDesired", stop doing that, but monitor for when we want it again?  or maybe just wait for another call?
	// TODO: connect to log of the total number of devices, with a min of 15?
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

func (rd *remoteDevice) writeFrame(frameType uint16, payload []byte) error {
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

	if small {
		if onlineSmallChannels > 0 {
			// write it which one?  round robin?  how to handle failues?
		} else {
			// write it down a large one because that's all we have
		}
	} else {
		if onlineLargeChannels > 0 {
			// write it which one?  round robin?  how to handle failues?
		} else {
			// write it down a small one because that's all we have
		}
	}

	// if some fail, properly dispose of them and try other sockets while kicking off other dials.
	// if this device is totally offline however, some way to communicate that up the connection
	// group is needed
	return nil
}

func (rd *remoteDevice) dial() {
}

func (rd *remoteDevice) insert(connection net.Conn) {
}

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
			go group.maintainConnection()
		}
	}
}
