package chat

import (
	"time"

	log "github.com/sirupsen/logrus"
)

func (b *Bounce) NetworkOnline() bool {
	return b.networkIsOnline.Load()
}

func (b *Bounce) networkOnline() {
	b.networkIsOnline.Store(true)
	// CompareAndSwap rather than test-and-set: the network calls this back on its
	// own goroutine and may do so more than once, and two callbacks that both pass
	// a plain check would each start a second accept loop and peer loop.
	if b.networkHasBeenOnline.CompareAndSwap(false, true) {
		// acceptConnections is deliberately not a tracked background task: it blocks in
		// network.Accept, which cannot be interrupted, so joining it would stall Shutdown.
		// It returns via its own shutdownStarted checks instead.
		go b.acceptConnections()
		if !b.encrypted {
			b.background(b.peer)
		}
	}
	if !b.encrypted {
		b.auditPeers()
		b.ui.NetworkOnline()
	}
}

func (b *Bounce) networkOffline() {
	b.networkIsOnline.Store(false)

	// The network only says this once nothing can be read any more, so every remote device entry is stale.  They
	// are cleared now, along with the dial cooldowns, so that the audit run by the online report that follows a
	// recovery dials into a pool that survives rather than one about to be thrown away.
	b.devicePool.lastDialMutex.Lock()
	b.devicePool.lastDial = make(map[string]time.Time)
	b.devicePool.lastFailedDial = make(map[string]time.Time)
	b.devicePool.lastDialMutex.Unlock()
	b.devicePool.deviceMutex.Lock()
	b.devicePool.devices = make(map[string]*remoteDevice)
	b.devicePool.deviceMutex.Unlock()

	if !b.encrypted {
		b.ui.NetworkOffline()
	}
}

func (b *Bounce) acceptConnections() {
	defer func() {
		if r := recover(); r != nil {
			errString := "recovered a panic while accepting a connection, this can occur when the network returns a non-fatal Accept error but the next attempt causes a panic in the network provider"
			if b.shutdownStarted.Load() {
				log.Error(errString)
			} else {
				log.Fatal(errString)
			}
		}
	}()
	for {
		conn, err, fatal := b.network.Accept()
		if err != nil {
			if fatal {
				if b.shutdownStarted.Load() {
					return
				} else {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Fatal("fatal error accepting connection")
				}
			} else {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Debug("error accepting connection")
			}
		} else {
			log.WithFields(log.Fields{
				"peer": conn.RemoteAddr().String(),
			}).Debug("accepted connection")
		}
		if conn == nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"fatal": fatal,
			}).Error("accepted nil connection")
		} else {
			go b.insertConnectionIntoDevicePool(conn)
		}
	}
}
