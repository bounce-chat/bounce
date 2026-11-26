package fyneui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/bounce-chat/bounce/chat"
	log "github.com/sirupsen/logrus"
)

func (ui *ui) DeviceOnline(id chat.ID) {
	ui.devices.online(id)
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })
}

func (ui *ui) DeviceOffline(id chat.ID) {
	ui.devices.offline(id)
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })
}

func (ui *ui) DeviceAdded(d chat.Device) {
	ui.devices.add(&d)
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })
}

func (ui *ui) DeviceRevoked(id chat.ID) {
	if ui.devices.isLocal(id) {
		fyne.DoAndWait(func() {
			ui.widgets.networkOfflineWarning.SetText("This device has been revoked")
			ui.widgets.networkOfflineWarning.Importance = widget.DangerImportance
			ui.widgets.networkOfflineWarning.Show()
			ui.widgets.networkOfflineWarning.Refresh()
		})
	} else {
		ui.devices.remove(id)
		fyne.DoAndWait(func() { ui.updateDeviceStatus() })
	}
}

func (ui *ui) DeviceRenamed(id chat.ID, name string) {
	ui.devices.rename(id, name)
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })
}

func (ui *ui) DeviceLastSeen(id chat.ID, timestamp int64) {
	ui.devices.updateLastSeen(id, timestamp)
	fyne.DoAndWait(func() { ui.updateDeviceStatus() })
}

// The warning showing for each encrypted device we cannot manage.  The engine reports such a device every time it
// asks the device for its key and gets one we do not hold, so the warning is shown once and left up until it is
// answered or the device can be managed again.  Only touched on the UI thread.
var unmanageableWarnings = make(map[chat.ID]dialog.Dialog)

func (ui *ui) EncryptedDeviceUnmanageable(id chat.ID) {
	ui.devices.Lock()
	d, ok := ui.devices.devices[id]
	ui.devices.Unlock()
	if !ok {
		log.WithFields(log.Fields{
			"device_id": id,
		}).Warn("unknown device is not manageable")
		return
	}

	fyne.Do(func() {
		if _, showing := unmanageableWarnings[id]; showing {
			return
		}

		warning := dialog.NewCustomConfirm(
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
		)
		unmanageableWarnings[id] = warning
		ui.showDialog(warning, func() {
			delete(unmanageableWarnings, id)
		})
	})
}

func (ui *ui) EncryptedDeviceManageable(id chat.ID) {
	fyne.Do(func() {
		warning, showing := unmanageableWarnings[id]
		if showing {
			// Hiding it answers it with "Keep Device"
			warning.Hide()
		}
	})
}
