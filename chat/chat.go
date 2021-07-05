package chat

import (
	"context"
	"os"
	"os/signal"

	"github.com/hkparker/bounce/network"
	"github.com/hkparker/bounce/protocol"
	"github.com/hkparker/bounce/ui"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

func Start(network network.BounceNetwork, ui ui.BounceUI) error {
	chat := &BounceChat{}
	grpcServer := grpc.NewServer()

	// Start the network
	err := network.Start()
	if err != nil {
	}

	// Serve the Bounce protocol on the network
	protocol.RegisterBounceServer(grpcServer, chat)
	handleInterrupts(grpcServer)

	// Start the gRPC server in a goroutine
	go func() {
		network.ServeGRPC(grpcServer)
		//err = network.ServeGRPC(grpcServer)
		//if err != nil {
		//	return err
		//}
	}()

	// Run the UI and block
	ui.Run()
	// Once the UI is closed, stop the server
	grpcServer.GracefulStop()

	return nil
}

func handleInterrupts(server *grpc.Server) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	go func() {
		for s := range c {
			log.WithFields(log.Fields{
				"at":     "chat.handleInterruptsGracefully",
				"signal": s.String(),
			}).Info("signal received to kill process, shutting down")
			server.GracefulStop()
			//ui.Stop()
		}
	}()
}

// Actual object that implements the protocol
type BounceChat struct {
	// TODO some connection to the gRPC server so the UI can close it
}

func (bounce *BounceChat) ReceiveMessage(context.Context, *protocol.ChatMessage) (*protocol.Errors, error) {
	return &protocol.Errors{}, nil
}
