package chat

import (
	"net"

	"google.golang.org/grpc"
)

//
// A bounce address is the public key and dialable address of another
// bounce device on an overlay network.  Not all networks have this
// property, Bounce was designed with I2P or Tor hidden services v3
// in  mind.
//
// Many networks, like libp2p, use a hash of the public key as the
// dialable address.  Bounce would need to be expanded to support
// public key retrival and verification in that design, and may do
// so in the future as needed.
//
//type BounceAddress string

//
// Any overlay network that can satisfy this interface can host Bounce
//
type BounceNetwork interface {
	LoadConfig(string)
	Start() error
	ServeGRPC(*grpc.Server) error
	Dial(address string) (*net.Conn, error)
	VerifySignature(string, []byte) error // TODO: better to have a "get public key" function and do this in the database?  Can support more networks but each has different key format
	//Sign
	//IsValidAddress
	Shutdown()
}
