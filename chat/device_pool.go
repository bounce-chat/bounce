package chat

import (
	"net"
	"sync"

	"github.com/google/uuid"
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

func newDevicePool() *devicePool {
	return &devicePool{
		groups: make(map[string]*connectionGroup),
		users:  make(map[uuid.UUID]*connectionGroup),
		//lookup: make(map[string]*remoteDevice),
		sync: &connectionGroup{},
	}
	// TODO: go try to maintain connections to sync devices
}

func (dp *devicePool) insert(connections net.Conn) { // TODO: or: bounce.insertIntoDevicePool(conn)

}

func (dp *devicePool) remove(connections net.Conn) {

}

type connectionGroup struct { // TODO: need one for users vs threads?  maybe not, for large user groups have a cap as well?
	connecting    bool
	remoteDevices []*remoteDevice
}

// TODO: create one of these for each device on startup and only dial as needed?
// TODO: Accept() should create one of these and insert it into the pool, then read frames
type remoteDevice struct {
	device       device
	queue        []*pendingFrame
	highPriority []*remoteConnection
	lowPriority  []*remoteConnection
	// some sort of state for it we're trying to connect or just waiting on incoming
	// sockets (len(queue) > 0 would be one condition)
}

func newRemoteDevice(device device) *remoteDevice {
	return &remoteDevice{
		device: device,
	}
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
		group.connecting = true
		// TODO: redial if needed
	} else {
		group = &connectionGroup{
			connecting: true,
		}
		bounce.devicePool.users[u.ID] = group
		for _, device := range u.Devices { // TODO: random selection if group size is too large?  database LIMIT query?
			userDevice := newRemoteDevice(device)
			go userDevice.dial()
			group.remoteDevices = append(group.remoteDevices, userDevice)
		}
	}
}
