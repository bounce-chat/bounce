package chat

import (
	"net"

	"github.com/hkparker/bounce/network"
)

var deviceConnections = make(map[network.BounceAddress]*net.Conn)

func Start(network network.BounceNetwork) {
	// Start the UI

	// Connect everything to the UI (to show network startup logs / status)

	// Start the network
	err := network.Start()
	if err != nil {
	}

	// attach grpc server to network
}
