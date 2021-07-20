package chat

import (
	"os"
	"os/signal"

	"github.com/hkparker/bounce/protocol"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// Actual object that implements the protocol
type BounceChat struct {
	configDirectory string
	userInterface   BounceUI
	network         BounceNetwork
	grpcServer      *grpc.Server
}

//
// The main entrypoint for starting the Bounce chat engine, blocks until the user interface
// is closed, the network reaches a fatal error, or the process is sent an interrupt.
//
func Start(network BounceNetwork, ui BounceUI) {
	bounce := &BounceChat{
		configDirectory: getConfigDirectory(),
		userInterface:   ui,
		network:         network,
		grpcServer:      grpc.NewServer(),
	}
	//bounce.openDatabase()
	bounce.network.LoadConfig(bounce.configDirectory)
	bounce.userInterface.Build(bounce.configDirectory)
	bounce.userInterface.RegisterCallbacks(Callbacks{
		SendMessage:                sendMessage,
		AddUserToGroup:             addUserToGroup,
		RenameGroup:                renameGroup,
		ChangeNotificationSettings: changeNotificationSettings,
	})
	//bounce.userInterface.LoadInitialState(bounce.buildInitialState())

	// Start the network and attach gRPC server in a goroutine
	//go bounce.runNetwork() // TODO: just disabled for now for UI prototyping
	// TODO: delete this, just for testing interactions during prototyping
	go simulate(ui)

	// Run the UI and block
	bounce.userInterface.Run()
	// Once the UI is closed, stop the server
	bounce.grpcServer.GracefulStop()
	bounce.network.Shutdown()
}

//
// Start the network and serve the Bounce protocol over gRPC.  If the network
// encounters a fatal error, close the user interface, exiting the application
//
func (chat *BounceChat) runNetwork() {
	// When this function returns the gRPC server will be stopped and it
	// will be time to close the user interface
	defer chat.userInterface.Quit()
	// TODO: rather than return and close the UI, call into the UI that the network has failed
	// so that it can be displayed

	// Start the network router
	err := chat.network.Start()
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "chat.runNetwork",
			"error": err.Error(),
		}).Error("unable to start network router")
		return
	} else {
		chat.userInterface.NetworkOnline()
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
