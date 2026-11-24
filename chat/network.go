package chat

import (
	"net"
)

// Any overlay network that can satisfy this interface can host Bounce
type Network interface {
	// Load the network keys from the config directory, generating them on the first run.  The provider keeps them
	// on disk encrypted under the given key, which the engine derives for this purpose alone, and encrypts keys it
	// finds stored in the clear by a version that did not.
	Load(configDirectory string, key []byte)

	// Start is the main entry point to a network provider.  It is responsible for connecting to the overlay network and
	// maintain a connection between network failures, and can block until Shutdown().  After Start() is called, the
	// network must continue to run, even between failures in the device's internet connection.  Start() is only called
	// once, and the callbacks are only used to update user interface indications of network status.
	Start(callbacks NetworkCallbacks)

	// Get the address of this device.  This must work even if the network has not been started or is offline, by generating
	// the address from the stored private key.
	Address() string

	// Name returns the stable identifier for this overlay network (for example "tor").  It must equal the
	// Network() reported by the net.Addr of the connections this provider accepts and dials, so that a device's
	// self-assigned network matches the network its peers observe for it.
	Name() string

	// Accept an incoming connection on the network.  Returns the net.Conn, any errors, and a boolean indicating if the
	// error was fatal.  Non-fatal errors will result in another immediate call to Accept, so Accept should block while
	// the network is offline, or at least have an internal cooldown sleep.
	Accept() (conn net.Conn, err error, fatal bool)

	// Attempt to dial an address on the network
	Dial(address string) (net.Conn, error)

	// Sign data with the private key of this device
	Sign(data []byte) []byte

	// Verify the signature of data signed by a device with the provided address
	VerifySignature(address string, data []byte, signature []byte) bool

	// Gracefully stop the network router
	Shutdown()
}

// Callbacks the network can use to inform the chat engine of state changes
type NetworkCallbacks struct {
	// NetworkOnline must be called the first time the network becomes available,
	// and any time the network recovers from an offline state
	NetworkOnline func()

	// NetworkOffline should be called when the network believes there is no
	// internet access.  The engine treats every connection as dead from then on,
	// so the provider should only say so once nothing can be read any more
	NetworkOffline func()
}
