package chat

import (
	"bytes"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type remoteDevice struct {
	connectedSockets       atomic.Int64
	messages               chan sendable
	chunks                 chan sendable
	shutdownReceivers      map[uuid.UUID]chan bool
	shutdownReceiversMutex sync.Mutex
	closer                 sync.WaitGroup
}

func newRemoteDevice() *remoteDevice {
	return &remoteDevice{
		connectedSockets:  atomic.Int64{},
		messages:          make(chan sendable),
		chunks:            make(chan sendable),
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

func (rd *remoteDevice) chunkDuty(writer uuid.UUID) bool {
	// Whichever writer has the lowest ID is responsible for sending chunks
	rd.shutdownReceiversMutex.Lock()
	defer rd.shutdownReceiversMutex.Unlock()

	if len(rd.shutdownReceivers) == 1 {
		return true
	}

	uuidList := []uuid.UUID{}
	for id, _ := range rd.shutdownReceivers {
		uuidList = append(uuidList, id)
	}

	if len(uuidList) == 0 {
		return true
	}

	lowest := uuidList[0]
	for _, id := range uuidList {
		if bytes.Compare(id[:], lowest[:]) < 0 {
			lowest = id
		}
	}

	return writer == lowest
}

func (b *Bounce) getRemoteDevice(address string) *remoteDevice {
	b.devicePool.deviceMutex.Lock()
	defer b.devicePool.deviceMutex.Unlock()

	rd, ok := b.devicePool.devices[address]
	if !ok {
		rd = newRemoteDevice()
		b.devicePool.devices[address] = rd
	}
	return rd
}

func (b *Bounce) insertConnectionIntoDevicePool(conn net.Conn) {
	// Get the remote device
	peer := conn.RemoteAddr().String()
	rd := b.getRemoteDevice(peer)

	// Read and write frames from this socket
	go b.readFrames(conn)
	go b.writeFrames(rd, conn)

	// Associate this device with a user if there is one
	dev, ok := b.getDeviceFromAddress(conn.RemoteAddr().String())
	if ok {
		b.insertRemoteDeviceIntoPool(conn.RemoteAddr().String(), poolTypeUser, dev.UserID)
	}

	// Do a reference flow
	b.sendReferences(peer)

	// Request any file chunks for this peer if needed
	b.makeNextChunkRequests()
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

func (b *Bounce) getDeviceOrEncryptedSyncDevice(peer string) (bool, bool, device, encryptedSyncDevice) {
	var deviceIsKnown bool
	var deviceIsEncrypted bool
	var dev device
	var esd encryptedSyncDevice
	dev, deviceIsKnown = b.getDeviceFromAddress(peer)
	if !deviceIsKnown {
		err := b.database.First(&esd, "address = ?", peer).Error
		if err == nil {
			deviceIsKnown = true
			deviceIsEncrypted = true
		}
	} else {
		deviceIsEncrypted = false
	}

	return deviceIsKnown, deviceIsEncrypted, dev, esd
}

func (b *Bounce) readFrames(conn net.Conn) {
	handlers := b.getHandlers()
	peer := conn.RemoteAddr().String()
	profile, profileExists := b.currentUser()

	deviceIsKnown, deviceIsEncrypted, dev, esd := b.getDeviceOrEncryptedSyncDevice(peer)

	for {
		frameType, data, err := readFrame(conn) // TODO: just read the header first, make sure we want to read the rest in the context of the device (untrusted devices can't send large messages, etc)
		if err != nil {
			return
		}

		if _, revoked := b.devicePool.revokedDevices[peer]; revoked {
			// Drop all frames from revoked devices unless they are reference requests or keep alives
			if !(frameType == typeReferenceRequest || frameType == typeKeepAlive || frameType == typeAck) {
				return
			}
		}

		if blockedUser(dev.UserID) {
			conn.Close()
			return
		}

		if b.shutdownStarted.Load() {
			return
		}

		// Make sure that we can handle this type of frame without a profile if we don't have one
		if !profileExists {
			// Re-check if we have a profile if needed, just in case one was setup between the last frame and this frame
			profile, profileExists = b.currentUser()
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

		// Re-check if we're aware of this device if needed
		if !deviceIsKnown {
			deviceIsKnown, deviceIsEncrypted, dev, esd = b.getDeviceOrEncryptedSyncDevice(peer)
		}
		if deviceIsKnown && profileExists {
			lastSeen := time.Now().Unix()
			if deviceIsEncrypted {
				b.ui.DeviceLastSeen(esd.ID, lastSeen)
			} else {
				if dev.UserID == profile.ID {
					b.ui.DeviceLastSeen(dev.ID, lastSeen)
				}
			}

			table := "devices"
			if deviceIsEncrypted {
				table = "encrypted_sync_devices"
			}
			err := b.database.Table(table).Where("address = ?", peer).Updates(map[string]interface{}{"last_seen": lastSeen}).Error
			if err != nil {
				log.WithFields(log.Fields{
					"peer":  peer,
					"error": err.Error(),
				}).Error("error updating last time device was seen")
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
				br, _ := handler(thisPeer, thisData, false)
				if br != nil {
					b.markDeliveredTo(br, thisPeer)
					go b.sendAck(thisPeer, br.getType(), br.getID())
					b.broadcast(br)
				}
				b.runningHandlers.Done()
			}(peer, data)
		}
	}
}

func (b *Bounce) writeFrames(rd *remoteDevice, conn net.Conn) {
	if b.shutdownStarted.Load() {
		return
	}
	rd.connectedSockets.Add(1)
	b.updateUserOnlineStatus(conn.RemoteAddr().String())
	rd.closer.Add(1)
	defer rd.closer.Done()

	writerID := uuid.New()
	rd.shutdownReceiversMutex.Lock()
	shutdownReceiver := make(chan bool)
	rd.shutdownReceivers[writerID] = shutdownReceiver
	rd.shutdownReceiversMutex.Unlock()

	writeChunk := func(br sendable) error {
		if _, revoked := b.devicePool.revokedDevices[conn.RemoteAddr().String()]; revoked {
			// Only send revoked devices frames that are used to tell them they are revoked, and keep alives
			if !(br.getType() == typeReferenceOffer || br.getType() == typeCatchUp || br.getType() == typeUpdateDevice || br.getType() == typeKeepAlive || br.getType() == typeAck) {
				log.WithFields(log.Fields{
					"type": br.getType(),
					"peer": conn.RemoteAddr().String(),
				}).Warn("attempt to send unexpected frame to revoked device")
				return nil
			}
		}

		err := writeFrame(conn, br.getType(), br.getPayload())
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"peer":  conn.RemoteAddr().String(),
				"type":  br.getType(),
			}).Debug("error writing frame")
			rd.connectedSockets.Add(-1)
			b.updateUserOnlineStatus(conn.RemoteAddr().String())

			// If this socket died during a write, but there are other sockets that might still be alive,
			// send references for anything that might not have made it through this socket
			if rd.connectedSockets.Load() > 0 {
				go b.sendReferences(conn.RemoteAddr().String())
			}

			// We will no longer be reading shutdown signals from the remote device
			// so we remove our channel from the map
			rd.shutdownReceiversMutex.Lock()
			delete(rd.shutdownReceivers, writerID)
			rd.shutdownReceiversMutex.Unlock()
			return err
		}

		return nil
	}

	for {
		// Only one socket at a time is responsible for sending chunks, in order to keep other
		// frames delivering in a low latency way while a large file is being transferred
		if rd.chunkDuty(writerID) {
			select {
			case <-shutdownReceiver:
				log.WithFields(log.Fields{
					"peer": conn.RemoteAddr().String(),
				}).Debug("closing connection")
				rd.connectedSockets.Add(-1)
				if !b.shutdownStarted.Load() {
					b.updateUserOnlineStatus(conn.RemoteAddr().String())
				}
				conn.Close()
				return
			case br := <-rd.messages:
				err := writeChunk(br)
				if err != nil {
					return
				}
			case br := <-rd.chunks:
				err := writeChunk(br)
				if err != nil {
					return
				}
			}
		} else {
			select {
			case <-shutdownReceiver:
				log.WithFields(log.Fields{
					"peer": conn.RemoteAddr().String(),
				}).Debug("closing connection")
				rd.connectedSockets.Add(-1)
				if !b.shutdownStarted.Load() {
					b.updateUserOnlineStatus(conn.RemoteAddr().String())
				}
				conn.Close()
				return
			case br := <-rd.messages:
				err := writeChunk(br)
				if err != nil {
					return
				}
			}
		}
	}
}
