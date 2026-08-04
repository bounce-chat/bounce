package chat

import (
	"sync/atomic"

	log "github.com/sirupsen/logrus"
)

// haveAcceptedConnections records whether this process has ever accepted an
// inbound connection. It gates the restart monitor, so that a fresh install
// with no peers yet is never mistaken for a network that has gone dead.
//
// Atomic because the accept loop writes it while the monitor goroutine reads
// it on its own timer.
var haveAcceptedConnections atomic.Bool
var haveDialedConnections atomic.Bool

func (b *Bounce) NetworkOnline() bool {
	return b.networkIsOnline
}

func (b *Bounce) networkOnline() {
	b.networkIsOnline = true
	if !b.networkHasBeenOnline {
		b.networkHasBeenOnline = true
		go b.acceptConnections()
		if !b.encrypted {
			go b.peer()
		}
	}
	if !b.encrypted {
		b.auditPeers()
		b.ui.NetworkOnline()
	}
}

func (b *Bounce) networkOffline() {
	b.networkIsOnline = false
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
			haveAcceptedConnections.Store(true)
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
