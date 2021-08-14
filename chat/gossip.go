package chat

import (
	"net"

	"github.com/google/uuid"
)

type devicePool struct {
	groups map[string][]remoteDevice
	users  map[string][]remoteDevice // Map from user UUID to any connections to that user's devices
	sync   []remoteDevice
}

type remoteDevice struct {
	user        uuid.UUID
	address     string
	connections []net.Conn
}

func (bounce *Bounce) gossip() {
	// look up the state from the database
	// decide which devices should be dialed to integrate into the network
	// populate the device pool
}
