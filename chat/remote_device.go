package chat

import (
	"net"
	"sync"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type remoteDevice struct {
	connectedSockets       int
	messages               chan sendable
	shutdownReceivers      map[uuid.UUID]chan bool
	shutdownReceiversMutex sync.Mutex
	closer                 sync.WaitGroup
}

func newRemoteDevice() *remoteDevice {
	return &remoteDevice{
		connectedSockets:  0,
		messages:          make(chan sendable),
		shutdownReceivers: make(map[uuid.UUID]chan bool),
	}
}

func (rd *remoteDevice) shutdown() {
	rd.shutdownReceiversMutex.Lock()
	for _, receiver := range rd.shutdownReceivers {
		receiver <- true
	}
	rd.shutdownReceiversMutex.Unlock()
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
	peer := conn.RemoteAddr().String()
	rd := b.getRemoteDevice(peer)

	go b.readFrames(conn)
	go b.writeFrames(rd, conn)

	b.sendReferences(peer)
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

func (b *bounce) readFrames(conn net.Conn) {
	handlers := b.getHandlers()
	peer := conn.RemoteAddr().String()
	_, profileExists := b.currentUser()

	for {
		frameType, data, err := readFrame(conn) // TODO: just read the header first, make sure we want to read the rest in the context of the device (untrusted devices can't send large messages, etc)
		if err != nil {
			return
		}

		if b.shutdownStarted {
			return
		}

		// Make sure that we can handle this type of frame without a profile if we don't have one
		if !profileExists {
			// Re-check if we have a profile if needed, just in case one was setup between the last frame and this frame
			_, profileExists = b.currentUser()
		}
		if !profileExists {
			if !frameAllowedWithoutProfile(frameType) {
				log.WithFields(log.Fields{
					"frame_type": frameType,
					"peer":       peer,
				}).Warn("peer sent a frame that cannot be handeled when no profile exists")
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
				log.WithFields(log.Fields{
					"peer": peer,
					"type": frameType,
					"size": len(thisData),
				}).Debug("handling a frame")
				handler(thisPeer, thisData)
				b.runningHandlers.Done()
			}(peer, data)
		}
	}
}

func (b *bounce) writeFrames(rd *remoteDevice, conn net.Conn) {
	if b.shutdownStarted {
		return
	}
	rd.connectedSockets += 1
	rd.closer.Add(1)
	defer rd.closer.Done()

	writerID := uuid.New()
	rd.shutdownReceiversMutex.Lock()
	rd.shutdownReceivers[writerID] = make(chan bool)
	rd.shutdownReceiversMutex.Unlock()

	for {
		select {
		case <-rd.shutdownReceivers[writerID]:
			log.WithFields(log.Fields{
				"peer": conn.RemoteAddr().String(),
			}).Debug("closing connection")
			rd.connectedSockets -= 1
			conn.Close()
			return
		case br := <-rd.messages:
			err := writeFrame(conn, br.getType(), br.getPayload())
			if err != nil {
				rd.connectedSockets -= 1
				// TODO: if we now have 0 connections, let the UI know the user is offline

				// If this socket died during a write, but there are other sockets that might still be alive,
				// send references for anything that might not have made it through this socket
				if rd.connectedSockets > 0 {
					go b.sendReferences(conn.RemoteAddr().String())
				}

				// We will no longer be reading shutdown signals from the remote device
				// so we remove our channel from the map
				rd.shutdownReceiversMutex.Lock()
				delete(rd.shutdownReceivers, writerID)
				rd.shutdownReceiversMutex.Unlock()
				return
			}
		}
	}
}
