package chat

import (
	"bytes"
	"errors"
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

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const MutedForever = int64(-1)
const MaximumNameLength = 128
const MaximumMessageCharacters = 15000
const MaximumNotesCharacters = MaximumMessageCharacters

// This constant defines the amount of time a message can exist without a successful delivery
// before the chat engine gives up and stops attempting to deliver it
const undeliverableAfter = time.Duration(4 * 7 * 24 * time.Hour)

var startupTime = time.Now()

type Bounce struct {
	encrypted            bool
	platform             Platform
	database             *gorm.DB
	referenceDatabase    *gorm.DB
	ui                   UI
	network              Network
	referenceState       *referenceState
	devicePool           *devicePool
	consensusStore       *consensusStore
	chunkEngine          *chunkEngine
	userID               ID
	networkIsOnline      atomic.Bool
	networkHasBeenOnline atomic.Bool
	shutdownStarted      atomic.Bool
	runningHandlers      sync.WaitGroup
	shutdownMutex        sync.Mutex

	// done is closed once, by stopBackgroundTasks, to tell every engine-lifetime
	// loop to return. backgroundTasks counts those loops so Shutdown can join them.
	// Registration and closing both happen under backgroundMutex so that a task
	// starting concurrently with Shutdown can never call Add after Wait has begun.
	done              chan struct{}
	backgroundTasks   sync.WaitGroup
	backgroundMutex   sync.Mutex
	backgroundStopped bool

	// uiNotifications carries UI calls that must reach the UI in the order the engine decided them, without the
	// engine waiting on the UI.  One goroutine drains it; see notifyUI.
	uiNotifications chan func()

	// handlersMutex guards registration with runningHandlers so that an Add can never
	// overlap the Wait in Shutdown. See startHandler.
	handlersMutex   sync.Mutex
	handlersStopped bool

	// handlerSlots bounds the number of frame handlers running at once across all peers.
	// See maximumRunningHandlersGlobal in remote_device.go.
	handlerSlots chan struct{}
}

// background starts fn as an engine-lifetime goroutine that Shutdown will wait
// for. Tasks started after shutdown has begun are dropped rather than spawned.
func (b *Bounce) background(fn func()) {
	b.backgroundMutex.Lock()
	if b.backgroundStopped {
		b.backgroundMutex.Unlock()
		return
	}
	b.backgroundTasks.Add(1)
	b.backgroundMutex.Unlock()

	go func() {
		defer b.backgroundTasks.Done()
		fn()
	}()
}

// stopBackgroundTasks closes done, which every background loop selects on. Safe to
// call more than once, which matters because Shutdown can be reached twice.
func (b *Bounce) stopBackgroundTasks() {
	b.backgroundMutex.Lock()
	defer b.backgroundMutex.Unlock()

	if b.backgroundStopped {
		return
	}
	b.backgroundStopped = true
	close(b.done)
}

// startHandler registers a frame handler with runningHandlers, returning false once
// shutdown has begun, in which case the caller must not spawn the handler at all.
func (b *Bounce) startHandler() bool {
	b.handlersMutex.Lock()
	defer b.handlersMutex.Unlock()

	if b.handlersStopped {
		return false
	}
	b.runningHandlers.Add(1)
	return true
}

// stopHandlers closes the gate above so that no further handlers can register. It must
// return before Shutdown calls runningHandlers.Wait: waiting while holding handlersMutex
// would deadlock against a handler blocked in startHandler.
func (b *Bounce) stopHandlers() {
	b.handlersMutex.Lock()
	defer b.handlersMutex.Unlock()

	b.handlersStopped = true
}

// sleepOrDone waits for d, returning false if the engine shut down first. Used by
// background loops that sleep rather than tick, so shutdown does not wait out a sleep.
func (b *Bounce) sleepOrDone(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return true
	case <-b.done:
		return false
	}
}

// The main entrypoint for starting the Bounce chat engine.
//
// Everything the install keeps at rest is encrypted, so the first thing Open does is unlock it, and that can fail in
// ways a user interface has to act on: ErrPasswordRequired, ErrWrongPassword, ErrKeystoreSecretMissing,
// ErrKeystoreUnavailable, ErrKeyFileMissing and ErrKeyFileDamaged, matched with errors.Is.  When Open returns an
// error nothing has been started and it can simply be called again, which is how a password is asked for: call it
// with an empty password first, and again with what the user typed after ErrPasswordRequired.
func Open(ui UI, network Network, platform Platform, password string) (Engine, error) {
	if os.Getenv("REPORT_CALLER") == "true" {
		log.SetReportCaller(true)

	}
	if os.Getenv("DEBUG") == "true" {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}
	installLogHooks(platform.ConfigDirectory)
	runningEngines.Add(1)

	b := &Bounce{
		platform:       platform,
		network:        network,
		ui:             ui,
		referenceState: newReferenceState(),
		devicePool: &devicePool{
			devices:            make(map[string]*remoteDevice),
			groupPools:         make(map[ID][]*remoteDevice),
			userPools:          make(map[ID][]*remoteDevice),
			userOnlineStatus:   make(map[ID]bool),
			deviceOnlineStatus: make(map[ID]bool),
			lastDial:           make(map[string]time.Time),
			lastFailedDial:     make(map[string]time.Time),
			revokedDevices:     make(map[string]bool),
		},
		consensusStore: &consensusStore{
			groups: make(map[ID]*canonicalStack),
		},
		done:            make(chan struct{}),
		uiNotifications: make(chan func(), 1024),
		handlerSlots:    make(chan struct{}, maximumRunningHandlersGlobal),
	}
	b.ensureOnlyOneInstance()

	// Only the instance lock is held at this point, so a failure to unlock has nothing else to undo
	databaseFile := b.platform.ConfigDirectory + "/bounce.db"
	kf, keys, err := unlock(platform, databaseFile, password)
	if err != nil {
		b.releaseInstance()
		runningEngines.Add(-1)
		return nil, err
	}

	// An install that could not use the keystore before is moved onto it as soon as it can.  That, and a first
	// password set during the last run, leave a master key to be replaced, which is done here because nothing has
	// the database open yet.
	kf, keys = addKeystore(kf, platform, password, keys)
	if !kf.wrapped() {
		log.Warn("the data in this install is not protected on this device: it has no keystore and no password")
	}
	databaseKey, networkKey := keys.databaseKey(), keys.networkKey()
	if keys.previous != nil {
		databaseKey, networkKey = rollMasterKey(kf, platform, network, databaseFile, keys)
	}

	log.RegisterExitHandler(b.fatalShutdown)
	go b.handleInterrupts()

	b.openDatabase(databaseKey)
	b.openReferenceDatabase()
	b.network.Load(b.platform.ConfigDirectory, networkKey)
	b.loadChunkEngine()

	go func() {
		if !b.deviceIsRevoked() {
			b.network.Start(
				NetworkCallbacks{
					NetworkOnline:  b.networkOnline,
					NetworkOffline: b.networkOffline,
				},
			)
		}
	}()

	b.background(b.keepDraftsSynced)
	b.background(b.keepDatabasePruned)
	b.background(b.deliverUINotifications)

	return b, nil
}

// notifyUI queues a UI call to be made after every one queued before it.  Group consensus can decide several things
// about one group in quick succession, a new state and then its deletion for instance, and separate goroutines would
// let the UI see them in either order.  The queue keeps the engine's order without making it wait on the UI.  During
// shutdown the call is dropped, since the UI is going too.
func (b *Bounce) notifyUI(call func()) {
	select {
	case b.uiNotifications <- call:
	case <-b.done:
	}
}

func (b *Bounce) deliverUINotifications() {
	for {
		select {
		case <-b.done:
			return
		case call := <-b.uiNotifications:
			call()
		}
	}
}

// Gracefully stop all Bounce.  Used when a fatal error is encountered or the user interface is closed
func (b *Bounce) Shutdown() {
	if b.shutdownStarted.CompareAndSwap(false, true) {
		runningEngines.Add(-1)
		engineShutdowns.Add(1)
	}

	// Tell every engine-lifetime loop to return. They are joined further down, once
	// the connections they may be using have been closed.
	b.stopBackgroundTasks()

	// Save all drafts to the database
	draftMutex.Lock()
	for _, d := range draftCache {
		if !d.Saved {
			err := deleteEach[draft](b.database, "thread = ?", d.Thread)
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
	// logrus has called os.Exit, or because Start() returns).  A consequence of this locking behavior however
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

	// Make sure any network handlers finish running. Close the gate first, so that no
	// handler can register while the wait below is running.
	log.Info("waiting for currently running handlers to stop")
	b.stopHandlers()
	handlersStopped := make(chan bool, 1)
	go func() {
		b.runningHandlers.Wait()
		handlersStopped <- true
	}()
	select {
	case <-handlersStopped:
	case <-time.After(2 * time.Second):
		log.Warn("running handlers took more than 2 seconds to stop, giving up")
	}

	// Wait for the engine-lifetime background loops signalled above to return
	log.Info("waiting for background tasks to stop")
	backgroundTasksStopped := make(chan bool, 1)
	go func() {
		b.backgroundTasks.Wait()
		backgroundTasksStopped <- true
	}()
	select {
	case <-backgroundTasksStopped:
	case <-time.After(2 * time.Second):
		log.Warn("background tasks took more than 2 seconds to stop, giving up")
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
		log.Info("closing the database")
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
	pidFile := b.platform.ConfigDirectory + "/.pid"
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
		version += ", Build " + revision[:7]
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

	// Close the database connection, if the Fatal did not fire while it was being opened
	if b.database != nil {
		sqliteDB, err := b.database.DB()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error getting underlying database interface from gorm during fatal shutdown")
		} else if err = sqliteDB.Close(); err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error closing database during fatal shutdown")
		}
	}

	// Delete our PID file
	pidFile := b.platform.ConfigDirectory + "/.pid"
	err := os.Remove(pidFile)
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
	signal.Notify(c, os.Interrupt)
	for s := range c {
		log.WithFields(log.Fields{
			"at":     "chat.handleInterrupts",
			"signal": s.String(),
		}).Info("signal received to kill process, shutting down")
		// Stopping the user interface unblocks the main blocking call of the application,
		// which in turn shuts down the network handler and the network
		if b.encrypted {
			b.fatalShutdown()
			os.Exit(0)
		} else {
			b.ui.Quit()
		}
	}
}

// ensureOnlyOneInstance takes the instance lock, which is the .pid file in the config directory, or ends the process
// when another instance holds it.
//
// Creating the file is what takes the lock, so it is created exclusively: of two processes started together only one
// can create it, and the other goes on to find the first alive.  That matters most on a first run, when each would
// otherwise generate its own keys for the same install.  A file left behind by a process that is gone is removed and
// the creation tried again, so taking over a stale lock goes through the same exclusive step.
func (b *Bounce) ensureOnlyOneInstance() {
	// Let mobile operating systems handle this problem
	if runtime.GOOS == "android" || runtime.GOOS == "ios" {
		return
	}

	pidFile := b.platform.ConfigDirectory + "/.pid"
	for attempt := 0; attempt < 5; attempt++ {
		file, err := os.OpenFile(pidFile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			_, err = file.WriteString(strconv.Itoa(os.Getpid()))
			if err != nil {
				file.Close()
				log.WithFields(log.Fields{
					"error": err.Error(),
					"path":  pidFile,
				}).Fatal("error writing current pid file")
			}
			err = file.Close()
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"path":  pidFile,
				}).Fatal("error writing current pid file")
			}
			return
		}
		if !errors.Is(err, os.ErrExist) {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"file":  pidFile,
			}).Fatal("error creating PID file")
		}

		// Get the PID from the file.  A file that has gone again belonged to an instance that just closed.
		pidBytes, err := os.ReadFile(pidFile)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"file":  pidFile,
			}).Fatal("error reading PID file")
		}
		pid, err := strconv.Atoi(string(pidBytes))
		if err != nil {
			// An instance that is starting right now has created the file and may not have written to it yet, so
			// look once more before deciding the file is damaged
			time.Sleep(100 * time.Millisecond)
			pidBytes, err = os.ReadFile(pidFile)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"file":  pidFile,
				}).Fatal("error reading PID file")
			}
			pid, err = strconv.Atoi(string(pidBytes))
		}

		if err != nil {
			log.WithFields(log.Fields{
				"error":    err.Error(),
				"file":     pidFile,
				"contents": string(pidBytes),
			}).Error("pid file did not contain int")
		} else {
			process, err := os.FindProcess(pid)
			if err != nil {
				if runtime.GOOS != "windows" {
					log.WithFields(log.Fields{
						"pid":   pid,
						"error": err.Error(),
					}).Error("os.FindProcess returned an error on a non-windows system")
				}
			} else if runtime.GOOS == "windows" {
				log.WithFields(log.Fields{
					"pid": pid,
				}).Fatal("Another instance of Bounce is running.  Please close it, or if you are sure it is not running, delete the pid file and try again: ", pidFile)
			} else {
				err := process.Signal(syscall.Signal(0))
				if err == nil {
					log.WithFields(log.Fields{
						"pid": pid,
					}).Fatal("Another instance of Bounce is running.  Please close it, or if you are sure it is not running, delete the pid file and try again: ", pidFile)
				} else if err.Error() != "no such process" && err.Error() != "os: process already finished" {
					log.WithFields(log.Fields{
						"pid":           pid,
						"syscall_error": err.Error(),
					}).Fatal("Another instance of Bounce may be running.  Please close it, or if you are sure it is not running, delete the pid file and try again: ", pidFile)
				}
			}
		}

		// The file refers to a process that is gone.  Delete it only while it is still the file that was examined:
		// another instance may have taken the stale lock over in the meantime, and its file must not be removed.
		current, err := os.ReadFile(pidFile)
		if err == nil && bytes.Equal(current, pidBytes) {
			err = os.Remove(pidFile)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				log.WithFields(log.Fields{
					"path":  pidFile,
					"error": err.Error(),
				}).Fatal("error deleting old pid file")
			}
		}
	}

	log.WithFields(log.Fields{
		"file": pidFile,
	}).Fatal("Another instance of Bounce appears to be starting.  Please close it, or if you are sure it is not running, delete the pid file and try again")
}

// releaseInstance gives the instance lock back when an engine could not be opened.  An engine that did open gives it
// back in Shutdown.
func (b *Bounce) releaseInstance() {
	pidFile := b.platform.ConfigDirectory + "/.pid"
	err := os.Remove(pidFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		log.WithFields(log.Fields{
			"path":  pidFile,
			"error": err.Error(),
		}).Error("error deleting pid file")
	}
}
