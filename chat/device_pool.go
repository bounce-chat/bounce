package chat

import (
	"net"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
)

type pendingFrame struct {
	frameType uint16
	payload   []byte
}

type devicePool struct {
	groups map[string]*connectionGroup
	users  map[uuid.UUID]*connectionGroup // Map from user UUID to any connections to that user's devices
	//lookup map[string]*remoteDevice // TODO: from device UUID to connection.  Needed?
	sync *connectionGroup
}

func (bounce *Bounce) newDevicePool() *devicePool {
	devicePool := &devicePool{
		groups: make(map[string]*connectionGroup),
		users:  make(map[uuid.UUID]*connectionGroup),
		//lookup: make(map[string]*remoteDevice),
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
		syncDevices.remoteDevices = append(syncDevices.remoteDevices, newRemoteDevice(dev))
	}
	devicePool.sync = syncDevices

	// Create a connection group for each user
	var allUsers []user
	err = bounce.database.Model(&user{}).Preload(clause.Associations).Where("profile = ?", false).Find(&allUsers).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading all users")
	}
	for _, u := range allUsers {
		userDevices := &connectionGroup{
			connectionDesired: false, // Should be true if there are pending messages
		}
		for _, dev := range u.Devices {
			userDevices.remoteDevices = append(userDevices.remoteDevices, newRemoteDevice(dev))
		}
		devicePool.users[u.ID] = userDevices
	}

	// TODO: create connection groups for all all groups

	return devicePool
}

func (dp *devicePool) insert(connections net.Conn) { // TODO: or: bounce.insertIntoDevicePool(conn)

}

func (dp *devicePool) remove(connections net.Conn) {

}

//
// A conection group is a collection of devices that represent one gossip scope, such as a chat group, a remote user, or your devices
//
type connectionGroup struct { // TODO: need one for users vs threads?  maybe not, for large user groups have a cap as well?  need to async dial all connections until min met
	connectionDesired bool
	remoteDevices     []*remoteDevice
}

// TODO: create one of these for each device on startup and only dial as needed?
// TODO: Accept() should create one of these and insert it into the pool, then read frames
type remoteDevice struct {
	device      device
	queue       []*pendingFrame
	smallFrames []*remoteConnection
	largeFrames []*remoteConnection // TODO: determined by len(pendingFrame.payload)
	// some sort of state for it we're trying to connect or just waiting on incoming
	// sockets (len(queue) > 0 would be one condition)
}

func newRemoteDevice(device device) *remoteDevice {
	rd := &remoteDevice{
		device: device,
		queue:  []*pendingFrame{},
	}

	//go func () {
	//	for {
	//		// read from the queue and write to the correct socket
	//	}
	//}()

	return rd
}

func (rd *remoteDevice) dial() {
}

func (rd *remoteDevice) insert(connection net.Conn) {
}

type remoteConnection struct {
	sync.Mutex
	alive      bool
	connection net.Conn
}

func (rc *remoteConnection) writeFrame(frameType uint16, payload []byte) error {
	rc.Lock()
	defer rc.Unlock()

	err := writeFrame(rc.connection, frameType, payload)
	if err != nil {
		rc.alive = false
	}
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
		group = &connectionGroup{
			connectionDesired: true,
		}
		bounce.devicePool.users[u.ID] = group
		for _, device := range u.Devices { // TODO: random selection if group size is too large?  database LIMIT query?
			userDevice := newRemoteDevice(device)
			go userDevice.dial()
			group.remoteDevices = append(group.remoteDevices, userDevice)
		}
	}
}
