package chat

import (
	"net"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type remoteDevice struct {
	sync.Mutex
	messages chan sendable
	chunks   chan sendable
	sockets  []*socket
}

func newRemoteDevice() *remoteDevice {
	rd := &remoteDevice{
		messages: make(chan sendable),
		chunks:   make(chan sendable),
		sockets:  make([]*socket, 0),
	}

	return rd
}

func (rd *remoteDevice) connectedSockets() int {
	rd.Lock()
	defer rd.Unlock()

	count := 0
	for _, s := range rd.sockets {
		s.Lock()
		if s.alive && time.Since(s.lastRead) < 60*time.Second {
			count += 1
		}
		s.Unlock()
	}
	return count
}

func (rd *remoteDevice) pruneSockets() {
	rd.Lock()
	defer rd.Unlock()

	aliveSockets := []*socket{}
	for _, s := range rd.sockets {
		s.Lock()
		alive := s.alive
		recentRead := time.Since(s.lastRead) < 60*time.Second
		s.Unlock()
		if alive && recentRead {
			aliveSockets = append(aliveSockets, s)
		} else {
			s.close()
		}
	}
	rd.sockets = aliveSockets
	for i, s := range rd.sockets {
		s.Lock()
		if i == 0 {
			s.chunkDuty = true
		} else {
			s.chunkDuty = false
		}
		s.Unlock()
	}

	if len(rd.sockets) > connectionsPerDevice {
		extra := rd.sockets[len(rd.sockets)-1]
		rd.sockets = rd.sockets[:len(rd.sockets)-1]
		extra.close()
	}
}

func (rd *remoteDevice) addSocket(s *socket) {
	rd.Lock()
	defer rd.Unlock()

	rd.sockets = append(rd.sockets, s)
}

func (rd *remoteDevice) shutdown() {
	rd.Lock()
	defer rd.Unlock()
	for _, s := range rd.sockets {
		s.close()
	}
}

type socket struct {
	sync.Mutex
	underlying net.Conn
	alive      bool
	lastRead   time.Time
	chunkDuty  bool
	closer     chan bool
	closeOnce  sync.Once
}

func (s *socket) close() {
	s.closeOnce.Do(func() {
		s.Lock()
		s.alive = false
		s.Unlock()
		close(s.closer)
		s.underlying.Close()
	})
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

	s := &socket{
		underlying: conn,
		alive:      true,
		lastRead:   time.Now(),
		closer:     make(chan bool),
	}
	rd.addSocket(s)
	rd.pruneSockets()

	// Read and write frames from this socket
	go b.readFrames(s)
	go b.writeFrames(rd, s)

	if !b.encrypted {
		// Associate this device with a user if there is one
		dev, ok := b.getDeviceFromAddress(conn.RemoteAddr().String())
		if ok {
			b.insertRemoteDeviceIntoPool(conn.RemoteAddr().String(), poolTypeUser, dev.UserID)
		}

		// Do a reference flow
		b.sendReferences(peer)
	} else {
		b.challengeUnencryptedPeerForReferenceOffer(conn.RemoteAddr().String())
	}
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

func (b *Bounce) readFrames(s *socket) {
	handlers := b.getHandlers(b.encrypted)
	peer := s.underlying.RemoteAddr().String()

	var profile user
	var profileExists bool
	var deviceIsKnown bool
	var deviceIsEncrypted bool
	var deviceIsAnotherUsersEncryptedDevice bool
	var dev device
	var esd encryptedSyncDevice
	if !b.encrypted {
		profile, profileExists = b.currentUser()
		deviceIsKnown, deviceIsEncrypted, dev, esd = b.getDeviceOrEncryptedSyncDevice(peer)
		if !deviceIsKnown {
			encryptedDeviceCacheMutex.Lock()
			_, deviceIsAnotherUsersEncryptedDevice = encryptedDeviceCache[peer]
			encryptedDeviceCacheMutex.Unlock()
		}
	}

	for {
		frameType, data, err := readFrame(s.underlying, deviceIsKnown || b.encrypted || deviceIsAnotherUsersEncryptedDevice)
		if err != nil {
			s.close()
			return
		}
		s.Lock()
		s.lastRead = time.Now()
		s.Unlock()

		if b.devicePool.isRevoked(peer) {
			// Drop all frames from revoked devices unless they are reference requests or keep alives
			if !(frameType == typeReferenceRequest || frameType == typeKeepAlive || frameType == typeAck) {
				return
			}
		}

		if blockedUser(dev.UserID) {
			s.close()
			return
		}

		if b.shutdownStarted.Load() {
			return
		}

		if !b.encrypted {
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
		}

		handler, ok := handlers[frameType]
		if !ok {
			log.WithFields(log.Fields{
				"peer": peer,
				"type": frameType,
			}).Error("peer sent an unsupported frame type")
		} else {
			b.runningHandlers.Add(1)
			go func(thisPeer string, thisData []byte) {
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

func (b *Bounce) writeFrames(rd *remoteDevice, s *socket) {
	if b.shutdownStarted.Load() {
		return
	}
	b.updateUserOnlineStatus(s.underlying.RemoteAddr().String())
	encryptedHandlers := b.getHandlers(true)

	writeChunk := func(br sendable) error {
		if b.devicePool.isRevoked(s.underlying.RemoteAddr().String()) {
			// Only send revoked devices frames that are used to tell them they are revoked, and keep alives
			if !(br.getType() == typeReferenceOffer || br.getType() == typeCatchUp || br.getType() == typeUpdateDevice || br.getType() == typeKeepAlive || br.getType() == typeAck) {
				log.WithFields(log.Fields{
					"type": br.getType(),
					"peer": s.underlying.RemoteAddr().String(),
				}).Warn("attempt to send unexpected frame to revoked device")
				return nil
			}
		}

		// Make sure only frames that can be sent to encrypted devices get sent to encrypted devices
		encryptedDeviceCacheMutex.Lock()
		_, encryptedDevice := encryptedDeviceCache[s.underlying.RemoteAddr().String()]
		encryptedDeviceCacheMutex.Unlock()
		if encryptedDevice {
			if _, ok := encryptedHandlers[br.getType()]; !ok {
				log.WithFields(log.Fields{
					"type": br.getType(),
					"peer": s.underlying.RemoteAddr().String(),
				}).Error("refusing to send frame that cannot be handled on encrypted device to encrypted device")
				return nil
			}
		}

		err := writeFrame(s.underlying, br.getType(), br.getPayload())
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"peer":  s.underlying.RemoteAddr().String(),
				"type":  br.getType(),
			}).Debug("error writing frame")
			b.updateUserOnlineStatus(s.underlying.RemoteAddr().String())

			// If this socket died during a write, but there are other sockets that might still be alive,
			// send references for anything that might not have made it through this socket
			if rd.connectedSockets() > 0 && !b.encrypted {
				go b.sendReferences(s.underlying.RemoteAddr().String())
			}
			return err
		}

		return nil
	}

	for {
		// Only one socket at a time is responsible for sending chunks, in order to keep other
		// frames delivering in a low latency way while a large file is being transferred
		s.Lock()
		chunkDuty := s.chunkDuty
		s.Unlock()
		if chunkDuty {
			select {
			case <-s.closer:
				log.WithFields(log.Fields{
					"peer": s.underlying.RemoteAddr().String(),
				}).Debug("closing connection")
				if !b.shutdownStarted.Load() {
					b.updateUserOnlineStatus(s.underlying.RemoteAddr().String())
				}
				return
			case br := <-rd.messages:
				err := writeChunk(br)
				if err != nil {
					s.close()
					return
				}
			case br := <-rd.chunks:
				err := writeChunk(br)
				if err != nil {
					s.close()
					return
				}
			}
		} else {
			select {
			case <-s.closer:
				log.WithFields(log.Fields{
					"peer": s.underlying.RemoteAddr().String(),
				}).Debug("closing connection")
				if !b.shutdownStarted.Load() {
					b.updateUserOnlineStatus(s.underlying.RemoteAddr().String())
				}
				return
			case br := <-rd.messages:
				err := writeChunk(br)
				if err != nil {
					s.close()
					return
				}
			}
		}
	}
}
