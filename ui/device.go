package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
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

func (ui *ui) EncryptedDeviceUnmanagable(id uuid.UUID) {
	ui.devices.Lock()
	d, ok := ui.devices.devices[id]
	ui.devices.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"device_id": id,
		}).Warn("unknown device is not managable")
		return
	}

	fyne.Do(func() {
		ui.showDialog(
			dialog.NewCustomConfirm(
				"Encrypted Device Management Error",
				"Remove Device",
				"Keep Device",
				container.NewVBox(
					widget.NewLabel("An encrypted device management action failed because we don't have the correct key.  Remove this encrypted device?"),
					widget.NewLabel(d.Address),
				),
				func(remove bool) {
					if remove {
						ui.bounce.RevokeDevice(id)
					}
				},
				ui.window,
			),
			nil,
		)
	})
}

func (ui *ui) EncryptedDeviceManagable(id uuid.UUID) {
}
