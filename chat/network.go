package chat

import (
	"net"

	"google.golang.org/grpc"
)

//
// Any overlay network that can satisfy this interface can host Bounce
//
type BounceNetwork interface {
	LoadConfig(string)
	Start() error
	ServeGRPC(*grpc.Server) error
	Dial(address BounceAddress) (*net.Conn, error)
	VerifySignature(BounceAddress, []byte) error // TODO: better to have a "get public key" function and do this in the database?  Can support more networks but each has different key format
	//Sign
	//IsValidAddress
	Shutdown()
}
