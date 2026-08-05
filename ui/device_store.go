package ui

import (
	"sync"

	"github.com/bounce-chat/bounce/chat"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type deviceStore struct {
	sync.Mutex
	devices map[uuid.UUID]*chat.Device
	ordered []*chat.Device
	local   *chat.Device
}

func newDeviceStore() *deviceStore {
	return &deviceStore{
		devices: make(map[uuid.UUID]*chat.Device),
		ordered: []*chat.Device{},
	}
}

func (ds *deviceStore) add(d *chat.Device) {
	ds.Lock()
	defer ds.Unlock()

	_, exists := ds.devices[d.ID]
	if exists {
		return
	}

	ds.devices[d.ID] = d

	smaller := 0
	for _, existingDevice := range ds.ordered {
		if existingDevice.CreatedAt < d.CreatedAt {
			smaller++
		}
	}
	smallerDevices := ds.ordered[:smaller]
	largerDevices := ds.ordered[smaller:]
	ds.ordered = append(smallerDevices, append([]*chat.Device{d}, largerDevices...)...)

	if d.Local {
		ds.local = d
	}
}

func (ds *deviceStore) isLocal(id uuid.UUID) bool {
	ds.Lock()
	defer ds.Unlock()

	return ds.local != nil && ds.local.ID == id
}

func (ds *deviceStore) remove(id uuid.UUID) {
	ds.Lock()
	defer ds.Unlock()

	delete(ds.devices, id)
	newList := []*chat.Device{}
	for _, d := range ds.ordered {
		if d.ID != id {
			newList = append(newList, d)
		}
	}
	ds.ordered = newList
}

func (ds *deviceStore) all() []*chat.Device {
	ds.Lock()
	defer ds.Unlock()

	liveOrder := []*chat.Device{}

	for _, dev := range ds.ordered {
		if dev.Local {
			liveOrder = append(liveOrder, dev)
		}
	}

	for _, dev := range ds.ordered {
		if !dev.Local && dev.Online {
			liveOrder = append(liveOrder, dev)
		}
	}

	for _, dev := range ds.ordered {
		if !dev.Local && !dev.Online {
			liveOrder = append(liveOrder, dev)
		}
	}

	return liveOrder
}

func (ds *deviceStore) online(id uuid.UUID) {
	ds.Lock()
	defer ds.Unlock()

	d, ok := ds.devices[id]
	if !ok {
		log.WithFields(log.Fields{
			"id": id,
		}).Warn("device not found in device store while setting online")
		return
	}

	d.Online = true
}

func (ds *deviceStore) offline(id uuid.UUID) {
	ds.Lock()
	defer ds.Unlock()

	d, ok := ds.devices[id]
	if !ok {
		log.WithFields(log.Fields{
			"id": id,
		}).Warn("device not found in device store while setting offline")
		return
	}

	d.Online = false
}

func (ds *deviceStore) rename(id uuid.UUID, name string) {
	ds.Lock()
	defer ds.Unlock()

	d, ok := ds.devices[id]
	if !ok {
		log.WithFields(log.Fields{
			"id": id,
		}).Warn("device not found in device store while setting name")
		return
	}

	d.Name = name
}

func (ds *deviceStore) updateLastSeen(id uuid.UUID, timestamp int64) {
	ds.Lock()
	defer ds.Unlock()

	d, ok := ds.devices[id]
	if !ok {
		log.WithFields(log.Fields{
			"id": id,
		}).Warn("device not found in device store while setting last seen time")
		return
	}

	d.LastSeen = timestamp
}
