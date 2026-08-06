package chat

import (
	"io/ioutil"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const MutedForever = int64(-1)
const MaximumNameLength = 128
const MaximumMessageCharacters = 15000

// This constant defines the amount of time a message can exist without a successful delivery
// before the chat engine gives up and stops attempting to deliver it
const undeliverableAfter = time.Duration(4 * 7 * 24 * time.Hour)

var startupTime = time.Now()

type Bounce struct {
	encrypted             bool
	configDirectory       string
	database              *gorm.DB
	referenceDatabase     *gorm.DB
	ui                    UI
	network               Network
	devicePool            *devicePool
	consensusStore        *consensusStore
	chunkEngine           *chunkEngine
	postNotification      func(string, string, string, string, []byte)
	clearNotification     func(string)
	userID                uuid.UUID
	networkIsOnline       bool
	networkHasBeenOnline  bool
	shutdownStarted       atomic.Bool
	databasePruningTicker *time.Ticker
	pruningDatabase       sync.WaitGroup
	runningHandlers       sync.WaitGroup
	shutdownMutex         sync.Mutex
}

// The main entrypoint for starting the Bounce chat engine, blocks until the user interface
// is closed, the network reaches a fatal error, or the process is sent an interrupt.
func Open(ui UI, network Network, configDirectory string, postNotification func(id, title, content, openThread string, icon []byte), clearNotification func(string)) Engine {
	if runtime.GOOS == "android" {
		log.SetLevel(log.DebugLevel)
		if os.Getenv("REPORT_CALLER") == "true" {
			log.SetReportCaller(true)
		}
	} else {
		if os.Getenv("REPORT_CALLER") == "true" {
			log.SetReportCaller(true)

		}
		if os.Getenv("DEBUG") == "true" {
			log.SetLevel(log.DebugLevel)
		} else {
			log.SetLevel(log.InfoLevel)
		}
	}
	log.AddHook(&filehook{})

	b := &Bounce{
		configDirectory: configDirectory,
		network:         network,
		ui:              ui,
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
		consensusStore: &consensusStore{
			groups: make(map[uuid.UUID]*canonicalStack),
		},
		postNotification:  postNotification,
		clearNotification: clearNotification,
	}
	b.ensureOnlyOneInstance()
	log.RegisterExitHandler(b.fatalShutdown)
	go b.handleInterrupts()

	b.openDatabase()

	go func() {
		b.network.Load(b.configDirectory)
		b.openReferenceDatabase()
		go b.loadChunkEngine()
		if !b.deviceIsRevoked() {
			b.network.Start(
				NetworkCallbacks{
					NetworkOnline:  b.networkOnline,
					NetworkOffline: b.networkOffline,
				},
			)
		}
	}()

	go b.keepDraftsSynced()

	return b
}

// Gracefully stop all Bounce.  Used when a fatal error is encountered or the user interface is closed
func (b *Bounce) Shutdown() {
	b.shutdownStarted.Store(true)

	// Save all drafts to the database
	draftMutex.Lock()
	for _, d := range draftCache {
		if !d.Saved {
			err := b.database.Clauses(clause.Returning{}).Where("thread = ?", d.Thread).Delete(&draft{}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error deleting drafts")
			}

			d.Saved = true
			err = b.database.Create(d).Error
			if err != nil {
				log.WithFields(log.Fields{
					"thread": d.Thread,
					"error":  err.Error(),
				}).Error("error saving draft")
			}
		}
	}
	draftMutex.Unlock()

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

		var waiter sync.WaitGroup
		for _, rd := range b.devicePool.devices {
			waiter.Go(rd.shutdown)
		}
		waiter.Wait()
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
	if !testing.Testing() {
		// The shutdown function is supposed to stop all background processing, but currently does not do
		// a perfect job at this.  Closing the database while things are running in the background can
		// cause a fatal error.  This is currently only a problem while running tests, but TODO: create a
		// more thorough shutdown function and remove this testing check.
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
	}

	// Delete our PID file
	pidFile := b.configDirectory + "/.pid"
	err := os.Remove(pidFile)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  pidFile,
			"error": err.Error(),
		}).Error("error deleting pid file")
	}

	log.Info("stopped bounce")
}

// Injected with -ldflags -X for builds the Go toolchain does not stamp itself.
//
// `go build` records vcs.revision and vcs.modified in the binary, but only for a
// main package: `gomobile bind` produces a c-shared library from a library
// package and carries no VCS settings at all, which was verified with
// `go version -m` on the resulting libgojni.so. Without these the Android build
// would silently report a bare release version. See the android-bind target.
var buildRevision string
var buildModified string

func (b *Bounce) VersionString() string {
	version := "Bounce Version 0.3.0"

	// The toolchain's own record wins where it exists, so an ordinary `go build`
	// behaves exactly as it did before these variables were added.
	revision := ""
	modified := false
	buildInfo, ok := debug.ReadBuildInfo()
	if ok {
		for _, bs := range buildInfo.Settings {
			if bs.Key == "vcs.revision" {
				revision = bs.Value
				break
			}
		}

		for _, bs := range buildInfo.Settings {
			if bs.Key == "vcs.modified" {
				modified = bs.Value == "true"
				break
			}
		}
	}

	if revision == "" {
		revision = buildRevision
		modified = buildModified == "true"
	}

	if len(revision) > 7 {
		version += " , Build " + revision[:7]
	}
	if modified {
		version += "-modified"
	}

	return version
}

// Used by logrus when a fatal error is logged.  We can't cleanly shutdown everything on fatal error, since
// the shutdown function waits for running handlers to finish executing, and the fatal call might have happened
// in one of them.  So we just attempt to close the database and delete the PID file before allowing logrus
// to exit.
func (b *Bounce) fatalShutdown() {
	// Close the UI
	if !b.encrypted {
		b.ui.Quit()
	}

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
	pidFile := b.configDirectory + "/.pid"
	err = os.Remove(pidFile)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  pidFile,
			"error": err.Error(),
		}).Error("error deleting pid file")
	}
}

// Shut the app down if the process receives an interrupt
func (b *Bounce) handleInterrupts() {
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
		if b.encrypted {
			b.fatalShutdown()
			os.Exit(0)
		} else {
			b.ui.Quit()
		}
	}
}

func (b *Bounce) ensureOnlyOneInstance() {
	// Let mobile operating systems handle this problem
	if runtime.GOOS == "android" || runtime.GOOS == "ios" {
		return
	}

	pidFile := b.configDirectory + "/.pid"

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
		}).Error("pid file did not contain int")
		err = os.Remove(pidFile)
		if err != nil {
			log.WithFields(log.Fields{
				"path":  pidFile,
				"error": err.Error(),
			}).Fatal("error deleting old pid file")
		}
		return
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		if runtime.GOOS == "windows" {
			err = os.Remove(pidFile)
			if err != nil {
				log.WithFields(log.Fields{
					"path":  pidFile,
					"error": err.Error(),
				}).Fatal("error deleting old pid file")
			}
		} else {
			log.WithFields(log.Fields{
				"pid":   pid,
				"error": err.Error(),
			}).Error("os.FindProcess returned an error on a non-windows system")
		}
	} else {
		if runtime.GOOS == "windows" {
			log.WithFields(log.Fields{
				"pid": pid,
			}).Fatal("Another instance of Bounce is running.  Please close it, or if you are sure it is not running, delete the pid file and try again: ", pidFile)
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
				}).Fatal("Another instance of Bounce may be running.  Please close it, or if you are sure it is not running, delete the pid file and try again: ", pidFile)
			}
		}
	}
}
