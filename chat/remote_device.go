package chat

import (
	"net"
	"sync"

	log "github.com/sirupsen/logrus"
)

type remoteDevice struct {
	connectedSockets int
	messages         chan broadcastable
	shutdown         chan bool
	closer           sync.WaitGroup
}

func newRemoteDevice() *remoteDevice {
	return &remoteDevice{
		connectedSockets: 0,
		messages:         make(chan broadcastable),
		shutdown:         make(chan bool, 1),
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

func (b *bounce) readFrames(conn net.Conn) { // TODO: move to protocol or something else?
	handlers := b.getHandlers()
	peer := conn.RemoteAddr().String()
	// Get the peer address
	// reject it if it isn't a known device?  Maybe don't want to if introductions / group membership is out of order
	// If it isn't know perhaps we put it in some limited handshake flow for new devices
	for {
		frameType, data, err := readFrame(conn) // TODO: just read the header first, make sure we want to read the rest in the context of the device (untrusted devices can't send large messages, etc)
		if err != nil {
			return
		}
		// TODO: some type of filtering on which types of peers can send which types of messages
		handler, ok := handlers[frameType]
		if !ok {
			log.WithFields(log.Fields{
				"peer": peer,
				"type": frameType,
			}).Error("peer sent an unsupported frame type, disconnecting")
			conn.Close()
			return
		} else {
			go handler(peer, data)
		}
	}
}

func (b *bounce) writeFrames(rd *remoteDevice, conn net.Conn) {
	rd.connectedSockets += 1
	rd.closer.Add(1)
	defer rd.closer.Done()

	for {
		select {
		case <-rd.shutdown:
			rd.connectedSockets -= 1
			conn.Close()
			return
		case br := <-rd.messages:
			err := writeFrame(conn, br.getType(), br.getPayload())
			if err != nil {
				rd.connectedSockets -= 1
				// TODO: if we now have 0 connections, let the UI know the user is offline
				b.sendReferences(conn.RemoteAddr().String())
				return
			}
		}
	}
}
