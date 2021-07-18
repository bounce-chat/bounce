package chat

import (
	"os"
	"os/signal"
	"time"

	"github.com/hkparker/bounce/protocol"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

//TODO: delete this, just for testing UI interactions
func simulate(ui BounceUI) {
	//time.Sleep(3 * time.Second)
	ui.NetworkLoaded() // TODO: try after there's already stuff going on in the UI
	//time.Sleep(1 * time.Second)
	user1 := User{
		ID:   "1",
		Name: "Alice",
	}
	user2 := User{
		ID:   "2",
		Name: "Bob",
	}
	user3 := User{
		ID:   "3",
		Name: "Charlie",
	}
	user4 := User{
		ID:   "4",
		Name: "David",
	}
	ui.LoadUsers([]User{user1, user2, user3, user4})
	ui.LoadThread(Thread{
		ID:      "001",
		Name:    "Group with Alice and Bob",
		UserIDs: []string{user1.ID, user2.ID},
	})
	//time.Sleep(2 * time.Second)
	ui.LoadThread(Thread{
		ID:      "002",
		Name:    "Group with Bob and Charlie",
		UserIDs: []string{user2.ID, user3.ID},
	})
	//time.Sleep(3 * time.Second)
	ui.LoadThread(Thread{
		ID:      "4",
		Name:    "DM with David",
		UserIDs: []string{user4.ID},
	})
	go func() {
		for i := 0; i < 25; i++ {
			ui.ReceivedMessage(Message{ThreadID: "001", UserID: "1", Text: "hello this is from user 1"})
			time.Sleep(1 * time.Second)
		}
	}()
	go func() {
		for i := 0; i < 10; i++ {
			ui.ReceivedMessage(Message{ThreadID: "002", UserID: "2", Text: "hello this is from user 2"})
			time.Sleep(5 * time.Second)
		}
	}()

	ui.ReceivedMessage(Message{ThreadID: "4", UserID: "4", Text: "hello this is from user 4"})

	/*
		time.Sleep(5 * time.Second)
		fyneUI.NetworkDisconnected()
		time.Sleep(5 * time.Second)
		fyneUI.NetworkLoaded()
	*/
}

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
	bounce.network.LoadConfig(bounce.configDirectory) // TODO; move these into their start functions?
	bounce.userInterface.Build(bounce.configDirectory)
	// TODO: load database state into UI
	// TODO: hookup UI callbacks
	bounce.userInterface.SetOnMessageSent(dispatchMessage)

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
