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
	// Accept() cannot return an error because the network implementation must be self-healing.  In the event that the router
	// fails to accept a new connection internally, it must communicate this to the chat engine via callback then return new
	// connections again when the network is healthy.
	Accept() *net.Conn
	Dial(address BounceAddress) (*net.Conn, error)
	VerifySignature(BounceAddress, []byte) error // TODO: better to have a "get public key" function and do this in the database?  Can support more networks but each has different key format
	//Sign
	//IsValidAddress
	Shutdown()
}
