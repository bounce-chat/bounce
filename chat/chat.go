package chat

import (
	"os"
	"os/signal"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type Bounce struct {
	configDirectory string
	database        *gorm.DB
	userInterface   BounceUI
	network         BounceNetwork
	devicePool      *devicePool
}

//
// The main entrypoint for starting the Bounce chat engine, blocks until the user interface
// is closed, the network reaches a fatal error, or the process is sent an interrupt.
//
func Start(network BounceNetwork, ui BounceUI) {
	if os.Getenv("DEBUG") == "true" {
		log.SetReportCaller(true)
	}
	log.SetLevel(log.DebugLevel) // TODO: put behind the envar when ready

	bounce := &Bounce{
		configDirectory: getConfigDirectory(),
		userInterface:   ui,
		network:         network,
		devicePool: &devicePool{
			devices:      make(map[string]*remoteDevice),
			receivedAcks: make(map[string]bool),
			lastDial:     make(map[string]time.Time),
		},
	}
	go bounce.handleInterrupts()
	log.RegisterExitHandler(bounce.shutdown)

	bounce.openDatabase()

	bounce.network.LoadConfig(bounce.configDirectory)
	//bounce.network.RegisterCallbacks(NetworkCallbacks{
	//	NetworkOffline:
	//})

	bounce.userInterface.Build(
		bounce.configDirectory,
		UICallbacks{
			SendDirectMessage:          bounce.sendDirectMessage,
			SendGroupMessage:           bounce.sendGroupMessage,
			AddUserToGroup:             bounce.addUserToGroup,
			RenameGroup:                bounce.renameGroup,
			ChangeNotificationSettings: bounce.changeNotificationSettings,
			SetProfile:                 bounce.setProfile,
			ImportUser:                 bounce.importUser,
			ExportContact:              bounce.exportContact,
			UserConnectionDesired:      bounce.userConnectionDesired,
		},
	)
	// To make the user interface more responsive to open, we load the database in a goroutine
	// and call in to let the UI know it's done after.  Most of the time this will appear instant,
	// but with a very large database it would be nice to see the window open while it's loading.
	go bounce.userInterface.LoadInitialState(bounce.buildInitialState())

	go bounce.runNetwork()

	// Run the UI and block
	bounce.userInterface.Run()

	// Once the UI is closed, stop bounce
	bounce.shutdown()
}

//
// Start the network and serve the Bounce protocol over the provided network.
//
func (bounce *Bounce) runNetwork() {
	// Start the network router
	err := bounce.network.Start(NetworkCallbacks{
		NetworkOnline:  bounce.userInterface.NetworkOnline,
		NetworkOffline: bounce.userInterface.NetworkOffline,
	})
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
		//bounce.userInterface.NetworkOnline() // TODO: testing tor-internal online monitor

		// TODO: this is just for testing but move this somewhere were it runs only when a profile already exists
		var count int64
		bounce.database.Model(&user{}).Where("profile = ?", true).Count(&count)
		if count > 0 {
			go bounce.peer()
		}
	}

	// Serve the Bounce protocol on the network
	// TODO: make sure the intial database state exists in the UI before accepting new connections?
	for {
		// TODO: check if we're gracefully shutting down

		conn, err := bounce.network.Accept()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error accepting connection") // TODO: just fatal for testing right now
			return
		} else {
			// TODO: just logging for testing right now
			log.WithFields(log.Fields{
				"peer": conn.RemoteAddr().String(),
			}).Debug("accepted connection")
		}
		go bounce.insertConnectionIntoDevicePool(conn)
	}
}

//
// Gracefully stop all Bounce.  Used when a fatal error is encountered or the user interface is closed
//
func (bounce *Bounce) shutdown() {
	// Stop all running tasks and close all connections to remote devices
	// TODO

	// Shutdown the network
	bounce.network.Shutdown()

	// Close the database
	sqliteDB, err := bounce.database.DB()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting underlying database interface from gorm during shutdown")
	}
	err = sqliteDB.Close()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error closing database during shutdown")
	}

	// Close the user interface if it isn't already closed
	bounce.userInterface.Quit()
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
