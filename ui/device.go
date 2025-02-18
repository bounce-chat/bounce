package ui

import (
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
)

func (fyneUI *Fyne) DeviceOnline(id uuid.UUID) {
	fyneUI.devices.online(id)
	fyneUI.updateDeviceStatus()
}

func (fyneUI *Fyne) DeviceOffline(id uuid.UUID) {
	fyneUI.devices.offline(id)
	fyneUI.updateDeviceStatus()
}

func (fyneUI *Fyne) DeviceAdded(d chat.Device) {
	fyneUI.devices.add(&d)
	fyneUI.updateDeviceStatus()
}

func (fyneUI *Fyne) DeviceRevoked(id uuid.UUID) {
	fyneUI.devices.remove(id)
	fyneUI.updateDeviceStatus()
}

func (fyneUI *Fyne) DeviceRenamed(id uuid.UUID, name string) {
	fyneUI.devices.rename(id, name)
	fyneUI.updateDeviceStatus()
}

func (fyneUI *Fyne) DeviceLastSeen(id uuid.UUID, timestamp int64) {
	fyneUI.devices.updateLastSeen(id, timestamp)
	fyneUI.updateDeviceStatus()
}
