package fyneui

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/alecthomas/assert/v2"
	"github.com/bounce-chat/bounce/chat"
)

// The engine reports an encrypted device it cannot manage every time it finds that out again.  The warning is shown
// once however often that is, and is taken down when the device can be managed again.
func TestAnUnmanageableDeviceIsWarnedAboutOnceUntilItIsManageableAgain(t *testing.T) {
	app := test.NewApp()
	window := app.NewWindow("devices")
	ui := &ui{window: window, state: &state{}, devices: newDeviceStore()}
	device := chat.Device{ID: chat.NewID(), Address: "the address of the encrypted device"}
	ui.devices.add(&device)
	warnings := func() int {
		return len(window.Canvas().Overlays().List())
	}

	ui.EncryptedDeviceUnmanageable(device.ID)
	ui.EncryptedDeviceUnmanageable(device.ID)
	assert.Equal(t, 1, warnings())

	ui.EncryptedDeviceManageable(device.ID)
	assert.Equal(t, 0, warnings())

	// A device that stops being manageable again later is warned about again
	ui.EncryptedDeviceUnmanageable(device.ID)
	assert.Equal(t, 1, warnings())
}
