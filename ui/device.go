package ui

import (
	"fyne.io/fyne/v2"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
)

func (ui *ui) DeviceOnline(id uuid.UUID) {
	ui.devices.online(id)
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })
}

func (ui *ui) DeviceOffline(id uuid.UUID) {
	ui.devices.offline(id)
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })
}

func (ui *ui) DeviceAdded(d chat.Device) {
	ui.devices.add(&d)
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })
}

func (ui *ui) DeviceRevoked(id uuid.UUID) {
	ui.devices.remove(id)
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })
}

func (ui *ui) DeviceRenamed(id uuid.UUID, name string) {
	ui.devices.rename(id, name)
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })
}

func (ui *ui) DeviceLastSeen(id uuid.UUID, timestamp int64) {
	ui.devices.updateLastSeen(id, timestamp)
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })
}
