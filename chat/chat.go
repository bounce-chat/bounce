package chat

import (
	"os"
	"os/signal"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type Bounce struct {
	configDirectory      string
	database             *gorm.DB
	userInterface        BounceUI
	network              BounceNetwork
	devicePool           *devicePool
	networkIsOnline      bool
	networkHasBeenOnline bool
	shutdownStarted      bool
	shutdownMutex        sync.Mutex
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
	bounce.userInterface.LoadInitialState(bounce.buildInitialState()) // TODO: this should be in a goroutine so we can display loading until ready, but that causes bugs.  Unclear why.

	go bounce.network.Start(
		bounce.configDirectory,
		NetworkCallbacks{
			NetworkOnline:  bounce.networkOnline,
			NetworkOffline: bounce.networkOffline,
		},
	)
	go bounce.peer()

	// Run the UI and block
	bounce.userInterface.Run()

	// Once the UI is closed, stop bounce
	bounce.shutdown()
}

//
// Gracefully stop all Bounce.  Used when a fatal error is encountered or the user interface is closed
//
func (bounce *Bounce) shutdown() {
	// Logrus is going to call in here on a fatal error, then os.Exit.  If multiple fatal logs occur, which
	// is likely as the shutdown process is going to cause other fatal errors, the first one will spend some
	// time closing down the network while the second will return much faster.  This second fatal error will
	// then call os.Exit before the network is actually shut down and this function has returned.  Therefore
	// we make this shutdown process synchronous with a mutex lock.  In theory unlocking it isn't necessary
	// since we're going to os.Exit as soon as this function returns, but it just feels wrong to not unlock
	// it, and I wouldn't want to create the appearance of an accidental deadlock being possible.
	bounce.shutdownMutex.Lock()
	defer bounce.shutdownMutex.Unlock()

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
