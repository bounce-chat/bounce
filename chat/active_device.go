package chat

import (
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

var lastActiveReport = atomic.Int64{}
var mostRecentActiveReports = map[string]int64{}
var mostRecentActiveReportsMutex sync.Mutex

const minimumSecondsBetweenActiveReports = 10
const secondsUntilDeviceInactive = 25

type activeDevice struct{}

func (ad activeDevice) getType() uint16 {
	return typeActiveDevice
}

func (ad activeDevice) getPayload() []byte {
	return []byte("active")
}

func (b *Bounce) handleActiveDevice(peer string, _ []byte, _ bool) (broadcastable, bool) {
	peerDevice, ok := b.getDeviceFromAddress(peer)
	if !ok {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("received active report from unknown device")
		return nil, false
	}
	if !b.isSyncDevice(peerDevice) {
		log.WithFields(log.Fields{
			"peer": peer,
		}).Warn("received active report from non-sync device")
		return nil, false
	}

	mostRecentActiveReportsMutex.Lock()
	mostRecentActiveReports[peer] = time.Now().Unix()
	mostRecentActiveReportsMutex.Unlock()

	b.updateOtherDeviceActive()
	go func() {
		time.Sleep(secondsUntilDeviceInactive + 1*time.Second)
		b.updateOtherDeviceActive()
	}()

	return nil, false
}

func (b *Bounce) CurrentDeviceActive() {
	now := time.Now().Unix()
	if lastActiveReport.Load()+minimumSecondsBetweenActiveReports > now {
		return
	}
	lastActiveReport.Store(now)

	currentUser, exists := b.currentUser()
	if !exists {
		return
	}

	for _, dev := range currentUser.Devices {
		if dev.Address == b.network.Address() {
			continue
		}
		rd := b.getRemoteDevice(dev.Address)
		if rd.connectedSockets.Load() > 0 {
			go func(dst chan sendable, msg sendable) {
				dst <- msg
			}(rd.messages, activeDevice{})
		}
	}
}

func (b *Bounce) updateOtherDeviceActive() {
	mostRecentActiveReportsMutex.Lock()
	defer mostRecentActiveReportsMutex.Unlock()

	now := time.Now().Unix()
	for _, ts := range mostRecentActiveReports {
		if ts+secondsUntilDeviceInactive > now {
			b.ui.AnotherDeviceActive()
			return
		}
	}

	b.ui.NoOtherDeviceActive()
}
