package chat

import (
	"io/ioutil"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

//
// This constant defines the amount of time a message can exist without a successful delivery
// before the chat engine gives up and stops attempting to deliver it
//
const undeliverableAfter = time.Duration(7 * 24 * time.Hour)

type bounce struct {
	configDirectory       string
	database              *gorm.DB
	userInterface         UI
	network               Network
	devicePool            *devicePool
	userID                uuid.UUID
	networkIsOnline       bool
	networkHasBeenOnline  bool
	shutdownStarted       bool
	databasePruningTicker *time.Ticker
	pruningDatabase       sync.WaitGroup
	shutdownMutex         sync.Mutex
	dmExistenceCheck      sync.Mutex
	uldsExistenceCheck    sync.Mutex
	syncDeviceRequest     sync.Mutex
}

//
// The main entrypoint for starting the Bounce chat engine, blocks until the user interface
// is closed, the network reaches a fatal error, or the process is sent an interrupt.
//
func Start(network Network, ui UI) {
	if os.Getenv("DEBUG") == "true" {
		log.SetReportCaller(true)
	}
	log.SetLevel(log.DebugLevel) // TODO: put behind the envar when ready.  run in warn otherwise?

	ensureOnlyOneInstance()
	b := &bounce{
		configDirectory: getConfigDirectory(),
		userInterface:   ui,
		network:         network,
		devicePool: &devicePool{
			devices:      make(map[string]*remoteDevice),
			receivedAcks: make(map[string]bool),
			lastDial:     make(map[string]time.Time),
		},
	}
	log.RegisterExitHandler(b.shutdown)
	go b.handleInterrupts()

	log.Debug("opening database")
	b.openDatabase()

	log.Debug("building ui")
	b.userInterface.Build(
		b.configDirectory,
		UICallbacks{
			GetNewSyncString:                b.getNewSyncString,
			RequestToSync:                   b.requestToSync,
			SendDirectMessage:               b.sendDirectMessage,
			SendGroupMessage:                b.sendGroupMessage,
			AddUserToGroup:                  b.addUserToGroup,
			RenameGroup:                     b.renameGroup,
			SetDMNotificationEnabled:        b.setDMNotificationEnabled,
			GetDMNotificationEnabled:        b.getDMNotificationEnabled,
			ChangeGroupNotificationSettings: b.changeGroupNotificationSettings,
			SetProfile:                      b.setProfile,
			ImportUser:                      b.importUser,
			ExportContact:                   b.exportContact,
			UserConnectionDesired:           b.userConnectionDesired,
		},
	)

	log.Debug("building initial state")
	initialState := b.buildInitialState()
	log.Debug("loading initial state")
	b.userInterface.LoadInitialState(initialState) // TODO: this should be in a goroutine so we can display loading until ready, but that causes bugs.  Unclear why.

	go b.network.Start(
		b.configDirectory,
		NetworkCallbacks{
			NetworkOnline:  b.networkOnline,
			NetworkOffline: b.networkOffline,
		},
	)
	go b.peer()

	// Run the UI and block
	b.userInterface.Run()

	// Once the UI is closed, stop bounce
	b.shutdown()
}

//
// Gracefully stop all Bounce.  Used when a fatal error is encountered or the user interface is closed
//
func (b *bounce) shutdown() {
	// Logrus is going to call in here on a fatal error, then os.Exit.  If multiple fatal logs occur, which
	// is likely as the shutdown process is going to cause other fatal errors, the first one will spend some
	// time closing down the network and database while the second will return much faster.  This second fatal
	// error will then call os.Exit before the network is actually shut down and this function has returned.
	// Therefore we make this shutdown process synchronous with a mutex lock.  We intentionally never unlock,
	// so all future calls into shutdown will block indefinitely until the application exits (either because
	// logrus has called os.Exit, or because Start() returns).  A concequence of this locking behavior however
	// is that fatal errors cannot be called from within this function without deadlocking the application, so
	// any errors encountered here must be logged as errors.
	b.shutdownMutex.Lock()

	// Stop all running tasks and close all connections to remote devices
	log.Info("closing all remote connections")
	// TODO: stop the peer audit loop, wait for it to return
	for _, rd := range b.devicePool.devices {
		// TODO: need to ensure no more messages will write to these channels to prevent panic
		// TODO: alternatievly, have a separte channel to close the remote devices and always leave
		// the message channels open
		//rd.shutdown <- true
		close(rd.messages)
	}
	for _, rd := range b.devicePool.devices {
		rd.closer.Wait()
	}

	// Shutdown the network
	log.Info("stopping the network")
	networkShutdown := make(chan bool, 1)
	go func() {
		b.network.Shutdown()
		networkShutdown <- true
	}()

	select {
	case <-networkShutdown:
	case <-time.After(5 * time.Second):
		log.Warn("network shutdown took longer than 5 seconds, giving up")
	}

	// Close the database
	log.Info("closing the database")
	// Close the pruning ticker channel and wait for the database to no longer be pruning
	if b.databasePruningTicker != nil {
		b.databasePruningTicker.Stop()
	}
	b.pruningDatabase.Wait()
	// Close the database connection
	sqliteDB, err := b.database.DB()
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
	b.userInterface.Quit()

	// Delete our PID file
	pidFile := getConfigDirectory() + "/.pid"
	err = os.Remove(pidFile)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  pidFile,
			"error": err.Error(),
		}).Error("error deleting pid file")
	}

	log.Info("stopped bounce")
}

//
// Shut the app down if the process receives an interrupt
//
func (b *bounce) handleInterrupts() {
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
		b.userInterface.Quit()
	}
}

func ensureOnlyOneInstance() {
	pidFile := getConfigDirectory() + "/.pid"

	// If there's no PID file, make one and return
	_, err := os.Stat(pidFile)
	if os.IsNotExist(err) {
		// Write the current PID to file
		err = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0600) // TODO: sync/flush?
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"path":  pidFile,
			}).Fatal("error writing current pid file")
		}
		return
	}

	// If we can't stat the pid file, fatal error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"file":  pidFile,
		}).Fatal("error stating PID file")
	}

	// Get the PID from the file
	pidBytes, err := ioutil.ReadFile(pidFile)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"file":  pidFile,
		}).Fatal("error reading PID file")
	}

	pid, err := strconv.Atoi(string(pidBytes))
	if err != nil {
		log.WithFields(log.Fields{
			"error":    err.Error(),
			"file":     pidFile,
			"contents": string(pidBytes),
		}).Fatal("pid file did not contain int")
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		// Always true on unix systems, on windows an error means the process is not running
		// https://stackoverflow.com/a/15204759
		// TODO: delete the pid file and return here if on windows?
		log.WithFields(log.Fields{
			"pid":   pid,
			"error": err.Error(),
		}).Warn("error on os.FindProcess")
	} else {
		err := process.Signal(syscall.Signal(0))
		if err == nil {
			log.Fatal("Another instance of Bounce is running.  Please close it, or if you are sure it is not running, delete the pid file and try again: ", pidFile)
		} else if err.Error() == "no such process" || err.Error() == "os: process already finished" {
			// Delete the old pid file that refers to a dead process
			err = os.Remove(pidFile)
			if err != nil {
				log.WithFields(log.Fields{
					"path":  pidFile,
					"error": err.Error(),
				}).Fatal("error deleting old pid file")
			}
			// Write the current PID to file
			err = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0600) // TODO: sync/flush?
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"path":  pidFile,
				}).Fatal("error writing current pid file")
			}
		} else {
			log.WithFields(log.Fields{
				"syscall_error": err.Error(),
			}).Fatal("Another instance of Bounce is running.  Please close it, or if you are sure it is not running, delete the pid file and try again: ", pidFile)
		}
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
