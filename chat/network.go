package chat

import (
	"net"
)

//
// Any overlay network that can satisfy this interface can host Bounce
//
type BounceNetwork interface {
	LoadConfig(string)
	RegisterCallbacks(NetworkCallbacks)
	Start() error
	// Get the local address of this device
	Address() (string, error)
	// Accept() cannot return an error because the network implementation must be self-healing.  In the event that the router
	// fails to accept a new connection internally, it must communicate this to the chat engine via callback then return new
	// connections again when the network is healthy.
	Accept() net.Conn
	Dial(address BounceAddress) (*net.Conn, error)
	VerifySignature(BounceAddress, []byte) error // TODO: better to have a "get public key" function and do this in the database?  Can support more networks but each has different key format
	//Sign
	//IsValidAddress
	Shutdown()
}

//
// A bounce address is the public key and dialable address of another
// bounce device on an overlay network.  Not all networks have this
// property, Bounce was designed with I2P or Tor hidden services v3
// in  mind.  In networks where the dialable address is a hash of a
// public key, like libp2p, the network implementation will need to
// internally retrieve, store, and use the public key for the address.
//
type BounceAddress string

//
// The chat engine will provide these callbacks to a user interface
//
type NetworkCallbacks struct {
	NetworkOffline func()
}
