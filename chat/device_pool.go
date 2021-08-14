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

type remoteDevice struct {
	user    uuid.UUID
	address string
	// TODO: lock this or use a channel?
	connections []net.Conn
}
