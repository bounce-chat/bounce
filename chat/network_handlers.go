package chat

import (
	log "github.com/sirupsen/logrus"
)

func (bounce *Bounce) networkOnline() {
	bounce.networkIsOnline = true
	if !bounce.networkHasBeenOnline {
		bounce.networkHasBeenOnline = true
		go bounce.acceptConnections()
	}
	bounce.auditPeers()
	bounce.userInterface.NetworkOnline()
}

func (bounce *Bounce) networkOffline() {
	bounce.networkIsOnline = false
	bounce.userInterface.NetworkOffline()
}

func (bounce *Bounce) acceptConnections() {
	for {
		conn, err, fatal := bounce.network.Accept()
		if err != nil {
			if fatal {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("fatal error accepting connection")
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
		go bounce.insertConnectionIntoDevicePool(conn)
	}
}
