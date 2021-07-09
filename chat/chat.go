package chat

import (
	"os"
	"os/signal"

	"github.com/hkparker/bounce/network"
	"github.com/hkparker/bounce/protocol"
	"github.com/hkparker/bounce/ui"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// Actual object that implements the protocol
type BounceChat struct {
	configDirectory string
	userInterface   ui.BounceUI
	network         network.BounceNetwork
	grpcServer      *grpc.Server
}

func Start(network network.BounceNetwork, ui ui.BounceUI) {
	chat := &BounceChat{
		configDirectory: getConfigDirectory(),
		userInterface:   ui,
		network:         network,
		grpcServer:      grpc.NewServer(),
	}
	chat.network.Init(chat.configDirectory)
	chat.userInterface.Init(chat.configDirectory)

	// Start the network and attach gRPC server in a goroutine
	go chat.runNetwork()

	// Run the UI and block
	ui.Run()
	// Once the UI is closed, stop the server
	chat.grpcServer.GracefulStop()
	network.Shutdown()
}

//
// Start the network and serve the Bounce protocol over gRPC.  If the network
// encounters a fatal error, close the user interface, exiting the application
//
func (chat *BounceChat) runNetwork() {
	// When this function returns the gRPC server will be stopped and it
	// will be time to close the user interface
	defer chat.userInterface.Quit()

	// Start the network router
	err := chat.network.Start()
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "chat.runNetwork",
			"error": err.Error(),
		}).Error("unable to start network router")
		return
	} else {
		//chat.userInterface.NetworkOnline()
	}

	// Serve the Bounce protocol on the network
	protocol.RegisterBounceServer(chat.grpcServer, chat)
	go chat.handleInterrupts()
	err = chat.network.ServeGRPC(chat.grpcServer)
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "chat.runNetwork",
			"error": err.Error(),
		}).Error("error returned from gRPC server")
	}
}

//
// Shut the app down if the process receives an interrupt
//
func (chat *BounceChat) handleInterrupts() {
	//
	// Handle interrupts, from a Ctrl+C on the command
	// line or a kill signal elsewhere
	//
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	for s := range c {
		log.WithFields(log.Fields{
			"at":     "chat.handleInterruptsGracefully",
			"signal": s.String(),
		}).Info("signal received to kill process, shutting down")
		// Stopping the user interface unblocks the main blocking call of the appplication,
		// which in turn shuts down the gRPC server and the network
		chat.userInterface.Quit()
	}
}

func getConfigDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "configDirectory",
			"error": err.Error(),
		}).Fatal("error getting home directory")
	}

	return home + "/.bounce"
}
