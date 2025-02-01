package chat

import (
	"io/ioutil"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const MutedForever = int64(-1)
const MaximumNameLength = 128

//
// This constant defines the amount of time a message can exist without a successful delivery
// before the chat engine gives up and stops attempting to deliver it
//
const undeliverableAfter = time.Duration(4 * 7 * 24 * time.Hour)

type bounce struct {
	configDirectory       string
	database              *gorm.DB
	referenceDatabase     *gorm.DB
	userInterface         UI
	network               Network
	devicePool            *devicePool
	consensusStore        *consensusStore
	userID                uuid.UUID
	networkIsOnline       bool
	networkHasBeenOnline  bool
	shutdownStarted       atomic.Bool
	databasePruningTicker *time.Ticker
	pruningDatabase       sync.WaitGroup
	runningHandlers       sync.WaitGroup
	shutdownMutex         sync.Mutex
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
			devices:            make(map[string]*remoteDevice),
			groupPools:         make(map[uuid.UUID][]*remoteDevice),
			userPools:          make(map[uuid.UUID][]*remoteDevice),
			userOnlineStatus:   make(map[uuid.UUID]bool),
			deviceOnlineStatus: make(map[uuid.UUID]bool),
			lastDial:           make(map[string]time.Time),
			lastFailedDial:     make(map[string]time.Time),
			revokedDevices:     make(map[string]bool),
		},
	}
	log.RegisterExitHandler(b.fatalShutdown)
	go b.handleInterrupts()

	b.userInterface.Build(
		b.configDirectory,
		UICallbacks{
			GetNewSyncString:                  b.getNewSyncString,
			RequestToSync:                     b.requestToSync,
			GetNewAddUserString:               b.getNewAddUserString,
			RequestToAddUser:                  b.requestToAddUser,
			SendDirectMessage:                 b.sendDirectMessage,
			CreateGroup:                       b.createGroup,
			SendGroupMessage:                  b.sendGroupMessage,
			AddUser:                           b.addUser,
			RemoveUser:                        b.removeUser,
			RenameGroup:                       b.renameGroup,
			SetGroupRetention:                 b.setGroupRetention,
			ClearGroupChatHistory:             b.clearGroupChatHistory,
			SetGroupMutedUntil:                b.setGroupMutedUntil,
			PromoteAdmin:                      b.promoteAdmin,
			DemoteAdmin:                       b.demoteAdmin,
			RestrictUserManagement:            b.restrictUserManagement,
			UnrestrictUserManagement:          b.unrestrictUserManagement,
			RestrictGroupEdits:                b.restrictGroupEdits,
			UnrestrictGroupEdits:              b.unrestrictGroupEdits,
			RestrictPosting:                   b.restrictPosting,
			UnrestrictPosting:                 b.unrestrictPosting,
			DeleteGroup:                       b.deleteGroup,
			BlockGroup:                        b.blockGroup,
			SetProfile:                        b.setProfile,
			ImportUser:                        b.importUser,
			ExportContact:                     b.exportContact,
			UserConnectionDesired:             b.userConnectionDesired,
			GroupConnectionDesired:            b.groupConnectionDesired,
			SetDMRetention:                    b.setDMRetention,
			SetDMMutedUntil:                   b.setDMMutedUntil,
			ClearDMChatHistory:                b.clearDMChatHistory,
			TypingInDirectMessage:             b.typingInDirectMessage,
			TypingInGroup:                     b.typingInGroup,
			UpdateProfileName:                 b.updateProfileName,
			RevokeDevice:                      b.revokeDevice,
			RenameDevice:                      b.renameDevice,
			MarkAsRead:                        b.markAsRead,
			NeverAskForBatteryOptimizations:   b.neverAskForBatteryOptimizations,
			SetReadReceiptsByDefault:          b.setReadReceiptsByDefault,
			SetTypingIndicatorsByDefault:      b.setTypingIndicatorsByDefault,
			SetNewGroupRetention:              b.setNewGroupRetention,
			SetNewGroupRestrictUserManagement: b.setNewGroupRestrictUserManagement,
			SetNewGroupRestrictGroupEdits:     b.setNewGroupRestrictGroupEdits,
			SetNewGroupRestrictPosting:        b.setNewGroupRestrictPosting,
			SetGroupReadReceiptSettings:       b.setGroupReadReceiptSettings,
			SetGroupTypingIndicatorSettings:   b.setGroupTypingIndicatorSettings,
			SetDMReadReceiptSettings:          b.setDMReadReceiptSettings,
			SetDMTypingIndicatorSettings:      b.setDMTypingIndicatorSettings,
			MarkAllGroupMessagesAsRead:        b.markAllGroupMessagesAsRead,
			MarkAllDirectMessagesAsRead:       b.markAllDirectMessagesAsRead,
			SetDarkMode:                       b.setDarkMode,
		},
	)

	go func() {
		b.network.Load(b.configDirectory)
		b.openDatabase()
		b.createConsensusStore()
		b.openReferenceDatabase()
		go b.network.Start(
			NetworkCallbacks{
				NetworkOnline:  b.networkOnline,
				NetworkOffline: b.networkOffline,
			},
		)
		initialState := b.buildInitialState()
		b.userInterface.LoadInitialState(initialState)
	}()

	// Run the UI and block
	b.userInterface.Run()

	// Once the UI is closed, stop bounce
	b.shutdown()
}

//
// Gracefully stop all Bounce.  Used when a fatal error is encountered or the user interface is closed
//
func (b *bounce) shutdown() {
	b.shutdownStarted.Store(true)

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

	// Stop any new attempts to dial
	log.Info("waiting for peer auditing to finish")
	devicePoolAuditingFinished := make(chan bool, 1)
	go func() {
		b.devicePool.auditing.Lock()
		devicePoolAuditingFinished <- true
	}()
	select {
	case <-devicePoolAuditingFinished:
	case <-time.After(2 * time.Second):
		log.Warn("waiting for the device pool to finish auditing took more than 2 seconds, giving up")
	}

	// Stop all running tasks and close all connections to remote devices
	log.Info("closing all remote connections")
	connectionsClosed := make(chan bool, 1)
	go func() {
		b.devicePool.deviceMutex.Lock()
		defer b.devicePool.deviceMutex.Unlock()

		for _, rd := range b.devicePool.devices {
			rd.shutdown()
		}
		for _, rd := range b.devicePool.devices {
			rd.closer.Wait()
		}
		connectionsClosed <- true
	}()
	select {
	case <-connectionsClosed:
	case <-time.After(2 * time.Second):
		log.Warn("closing connections took longer than 2 seconds, giving up")
	}

	// Make sure any network handlers finish running
	log.Info("waiting for currently running handlers to stop")
	handlersStopped := make(chan bool, 1)
	go func() {
		b.runningHandlers.Wait()
		handlersStopped <- true
	}()
	select {
	case <-handlersStopped:
	case <-time.After(2 * time.Second):
		log.Warn("running handlers took more then 2 seconds to stop, giving up")
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
	case <-time.After(2 * time.Second):
		log.Warn("network shutdown took longer than 2 seconds, giving up")
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
// Used by logrus when a fatal error is logged.  We can't cleanly shutdown everything on fatal error, since
// the shutdown function waits for running handlers to finish executing, and the fatal call might have happened
// in one of them.  So we just attempt to close the database and delete the PID file before allowing logrus
// to exit.
//
func (b *bounce) fatalShutdown() {
	// Close the database connection
	sqliteDB, err := b.database.DB()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error getting underlying database interface from gorm during fatal shutdown")
	}
	err = sqliteDB.Close()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error closing database during fatal shutdown")
	}

	// Delete our PID file
	pidFile := getConfigDirectory() + "/.pid"
	err = os.Remove(pidFile)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  pidFile,
			"error": err.Error(),
		}).Error("error deleting pid file")
	}
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
	// Let mobile operating systems handle this problem
	if runtime.GOOS == "android" || runtime.GOOS == "ios" {
		return
	}

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
			log.WithFields(log.Fields{
				"pid": pid,
			}).Fatal("Another instance of Bounce is running.  Please close it, or if you are sure it is not running, delete the pid file and try again: ", pidFile)
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
				"pid":           pid,
				"syscall_error": err.Error(),
			}).Fatal("Another instance of Bounce is running.  Please close it, or if you are sure it is not running, delete the pid file and try again: ", pidFile)
		}
	}
}

func getConfigDirectory() string {
	var configDirectory string
	if testing.Testing() {
		configDirectory = os.TempDir() + "/bounce-test-" + uuid.New().String()
	} else if runtime.GOOS == "android" {
		// TODO: use /data/data/chat.bounce/ ?
		configDirectory = "/sdcard/Android/data/chat.bounce"
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "configDirectory",
				"error": err.Error(),
			}).Fatal("error getting home directory")
		}
		configDirectory = home + "/.bounce"
	}

	err := os.MkdirAll(configDirectory, 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  configDirectory,
			"error": err.Error(),
		}).Fatal("error creating config directory")
	}

	return configDirectory
}
