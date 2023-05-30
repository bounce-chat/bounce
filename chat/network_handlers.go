package chat

import (
	log "github.com/sirupsen/logrus"
)

func (b *bounce) networkOnline() {
	b.networkIsOnline = true
	if !b.networkHasBeenOnline {
		b.networkHasBeenOnline = true
		go b.acceptConnections()
		go b.peer()
	}
	b.auditPeers()
	b.userInterface.NetworkOnline()
}

func (b *bounce) networkOffline() {
	b.networkIsOnline = false
	b.userInterface.NetworkOffline()
}

func (b *bounce) acceptConnections() {
	defer func() {
		if r := recover(); r != nil {
			log.Fatal("recovered a panic while accepting a connection, this can occur when the network returns a non-fatal Accept error but the next attempt causes a panic in the network provider")
		}
	}()
	for {
		conn, err, fatal := b.network.Accept()
		if err != nil {
			if fatal {
				if b.shutdownStarted {
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
