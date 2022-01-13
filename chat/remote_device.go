package chat

import (
	"net"
	"sync"

	log "github.com/sirupsen/logrus"
)

type remoteDevice struct {
	connectedSockets int
	messages         chan broadcastable
	//shutdown         chan bool
	closer sync.WaitGroup
}

func newRemoteDevice() *remoteDevice {
	return &remoteDevice{
		connectedSockets: 0,
		messages:         make(chan broadcastable),
		//shutdown:         make(chan bool, 1),
	}
}

func (b *bounce) getRemoteDevice(address string) *remoteDevice {
	b.devicePool.deviceMutex.Lock()
	defer b.devicePool.deviceMutex.Unlock()

	rd, ok := b.devicePool.devices[address]
	if !ok {
		rd = newRemoteDevice()
		b.devicePool.devices[address] = rd
	}
	return rd
}

func (b *bounce) insertConnectionIntoDevicePool(conn net.Conn) {
	peerAddress := conn.RemoteAddr().String()
	rd := b.getRemoteDevice(peerAddress)

	go b.readFrames(conn)
	go b.writeFrames(rd, conn)

	b.sendReferences(peerAddress)
}

func frameAllowedWithoutProfile(frameType uint16) bool {
	allowedFrames := []uint16{
		typeSyncDeviceRequestAccepted,
		typeSyncDeviceRequestRejected,
		typeKeepAlive,
	}

	for _, allowed := range allowedFrames {
		if frameType == allowed {
			return true
		}
	}

	return false
}

func frameAllowedFromUnknownPeer(frameType uint16) bool {
	allowedFrames := []uint16{
		typeDevice,
		typeSyncDeviceRequest,
		typeSyncDeviceRequestRejected,
		typeSyncDeviceRequestAccepted,
		typeKeepAlive,
	}

	for _, allowed := range allowedFrames {
		if frameType == allowed {
			return true
		}
	}

	return false
}

func (b *bounce) readFrames(conn net.Conn) { // TODO: move to protocol or something else?
	handlers := b.getHandlers()
	peer := conn.RemoteAddr().String()

	for {
		frameType, data, err := readFrame(conn) // TODO: just read the header first, make sure we want to read the rest in the context of the device (untrusted devices can't send large messages, etc)
		if err != nil {
			return
		}

		// If Bounce is shutting down, we no longer want to handle any frames coming
		// from this connection
		if b.shutdownStarted {
			return
		}

		// Make sure that we can handle this type of frame without a profile if we don't have one
		_, profileExists := b.currentUser()
		if !profileExists {
			if !frameAllowedWithoutProfile(frameType) {
				log.WithFields(log.Fields{
					"frame_type": frameType,
					"peer":       peer,
				}).Warn("peer sent a frame that cannot be handeled when no profile exists")
				continue
			}
		}

		// Restrict the types of frames that can come from unknown peers
		_, peerExists := b.getDeviceFromAddress(peer)
		if !peerExists {
			if !frameAllowedFromUnknownPeer(frameType) {
				log.WithFields(log.Fields{
					"frame_type": frameType,
					"peer":       peer,
				}).Warn("unknown peer sent a frame that is not allowed from unknown peers")
				continue
			}
		}

		handler, ok := handlers[frameType]
		if !ok {
			log.WithFields(log.Fields{
				"peer": peer,
				"type": frameType,
			}).Error("peer sent an unsupported frame type, disconnecting")
			conn.Close()
			return
		} else {
			go func(thisPeer string, thisData []byte) {
				b.runningHandlers.Add(1)
				handler(thisPeer, thisData)
				b.runningHandlers.Done()
			}(peer, data)
		}
	}
}

func (b *bounce) writeFrames(rd *remoteDevice, conn net.Conn) {
	rd.connectedSockets += 1
	rd.closer.Add(1)
	defer rd.closer.Done()

	// TODO: shutdown signal channel not working
	//for {
	//	select {
	//	case <-rd.shutdown:
	//		rd.connectedSockets -= 1
	//		conn.Close()
	//		return
	//	case br := <-rd.messages:
	//		err := writeFrame(conn, br.getType(), br.getPayload())
	//		if err != nil {
	//			rd.connectedSockets -= 1
	//			// TODO: if we now have 0 connections, let the UI know the user is offline
	//			b.sendReferences(conn.RemoteAddr().String())
	//			return
	//		}
	//	}
	//}
	for br := range rd.messages {
		err := writeFrame(conn, br.getType(), br.getPayload())
		if err != nil {
			rd.connectedSockets -= 1
			// TODO: if we now have 0 connections, let the UI know the user is offline
			b.sendReferences(conn.RemoteAddr().String())
			return
		}
	}
	rd.connectedSockets -= 1
	conn.Close()
}
