package chat

import (
	"context"

	"github.com/hkparker/bounce/network"
	"github.com/hkparker/bounce/protocol"
	"google.golang.org/grpc"
)

func Start(network network.BounceNetwork) {
	// Start the UI

	// Connect everything to the UI (to show network startup logs / status)

	// Start the network
	err := network.Start()
	if err != nil {
	}

	grpcServer := grpc.NewServer()
	protocol.RegisterBounceServer(grpcServer, &BounceChat{})
	network.ServeGRPC(grpcServer)
	// attach grpc server to network
}

// Actual object that implements the protocol
type BounceChat struct {
	// TODO some connection to the gRPC server so the UI can close it
}

func (bounce *BounceChat) ReceiveMessage(context.Context, *protocol.ChatMessage) (*protocol.Errors, error) {
	return &protocol.Errors{}, nil
}
