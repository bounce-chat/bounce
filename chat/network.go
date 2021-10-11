package chat

import (
	"net"
)

//
// Any overlay network that can satisfy this interface can host Bounce
//
type BounceNetwork interface {
	LoadConfig(string)
	Start(callbacks NetworkCallbacks) error // TODO: don't return error, async and use callbacks to communicate state
	// Get the local address of this device
	Address() string
	// Accept() cannot return an error because the network implementation must be self-healing.  In the event that the router
	// fails to accept a new connection internally, it must communicate this to the chat engine via callback then return new
	// connections again when the network is healthy.
	Accept() (net.Conn, error)
	Dial(address string) (net.Conn, error)
	Sign([]byte) []byte
	VerifySignature(address string, data []byte, signature []byte) bool
	//IsValidAddress
	Shutdown()
}

//
// The chat engine will provide these callbacks to a user interface
//
type NetworkCallbacks struct {
	NetworkOnline  func()
	NetworkOffline func()
}
