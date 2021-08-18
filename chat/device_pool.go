package chat

import (
	"net"

	"github.com/google/uuid"
)

type devicePool struct {
	groups map[string][]*remoteDevice
	users  map[string][]*remoteDevice // Map from user UUID to any connections to that user's devices
	lookup map[string]*remoteDevice
	sync   []remoteDevice
}

func newDevicePool() *devicePool {
	return &devicePool{
		groups: make(map[string][]*remoteDevice),
		users:  make(map[string][]*remoteDevice),
		lookup: make(map[string]*remoteDevice),
		sync:   []remoteDevice{},
	}
}

func (dp *devicePool) insert(connections net.Conn) { // TODO: or: bounce.insertIntoDevicePool(conn)

}

func (dp *devicePool) remove(connections net.Conn) {

}

func (bounce *Bounce) dialUser(u user) {
	// if we've got an open connection we're all good
	// try to dial all the devices for this user if not
}

// TODO: create one of these for each device on startup and only dial as needed?
// TODO: Accept() should create one of these and insert it into the pool, then read frames
type remoteDevice struct {
	user   uuid.UUID
	device uuid.UUID
	// TODO: lock this or use a channel?
	connections []net.Conn
}
