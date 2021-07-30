package chat

import (
	"os"
	"os/signal"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type Bounce struct {
	configDirectory string
	database        *gorm.DB
	userInterface   BounceUI
	network         BounceNetwork
}

//
// The main entrypoint for starting the Bounce chat engine, blocks until the user interface
// is closed, the network reaches a fatal error, or the process is sent an interrupt.
//
func Start(network BounceNetwork, ui BounceUI) {
	bounce := &Bounce{
		configDirectory: getConfigDirectory(),
		userInterface:   ui,
		network:         network,
	}
	go bounce.handleInterrupts()
	bounce.openDatabase()

	bounce.network.LoadConfig(bounce.configDirectory)
	//bounce.network.RegisterCallbacks(NetworkCallbacks{
	//	NetworkOffline:
	//})

	bounce.userInterface.Build(bounce.configDirectory)
	bounce.userInterface.RegisterCallbacks(UICallbacks{
		SendMessage:                bounce.sendMessage,
		AddUserToGroup:             bounce.addUserToGroup,
		RenameGroup:                bounce.renameGroup,
		ChangeNotificationSettings: bounce.changeNotificationSettings,
		SetProfile:                 bounce.setProfile,
	})
	// To make the user interface more responsive to open, we load the database in a goroutine
	// and call in to let the UI know it's done after.  Most of the time this will appear instant,
	// but with a very large database it would be nice to see the window open while it's loading.
	go bounce.userInterface.LoadInitialState(bounce.buildInitialState())

	//go bounce.runNetwork() // TODO: just disabled for now for UI prototyping
	go simulate(ui) // TODO: delete this, just for testing interactions during prototyping

	// Run the UI and block
	bounce.userInterface.Run()

	// Once the UI is closed, stop the server
	// TODO: gracefully shut down our handlers and close the database
	bounce.network.Shutdown()
}

//
// Start the network and serve the Bounce protocol over the provided network.
//
func (bounce *Bounce) runNetwork() {
	// Start the network router
	err := bounce.network.Start()
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "chat.runNetwork",
			"error": err.Error(),
		}).Error("unable to start network router")
		// TODO: this shouldn't fail here.  Need a good UX for starting the app while the
		// internet is disconnected.
		// TODO: perhaps send a network failed callback which will let the user view their
		// messages, but adds a banner informing them the network is offline and disables
		// all chat entries
		return
	} else {
		//go bounce.gossip() // TODO: put this in the right place
		bounce.userInterface.NetworkOnline()
	}

	// Serve the Bounce protocol on the network
	// TODO: make sure the intial database state exists in the UI before accepting new connections?
	for {
		// TODO: check if we're gracefully shutting down
		bounce.handleIncomingConnection(bounce.network.Accept())
	}
}

//
// Shut the app down if the process receives an interrupt
//
func (bounce *Bounce) handleInterrupts() {
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
		// which in turn shuts down the network handler and the network
		bounce.userInterface.Quit()
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

	configDirectory := home + "/.bounce"

	err = os.MkdirAll(configDirectory, 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  configDirectory,
			"error": err.Error(),
		}).Fatal("error creating config directory")
	}

	return configDirectory
}
