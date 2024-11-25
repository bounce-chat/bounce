package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type settingsWidgets struct {
	sendReadReceiptsByDefault             *widget.Check
	sendTypingIndicatorsByDefault         *widget.Check
	defaultNewGroupRetention              *widget.Select
	defaultNewGroupRestrictUserManagement *widget.Check
	defaultNewGroupRestrictGroupEdits     *widget.Check
	defaultNewGroupRestrictPosting        *widget.Check
}

func (fyneUI *Fyne) showSettings() {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeSettings})
	}
	fyneUI.mainWindow.SetContent(fyneUI.settingsContainer)
	fyneUI.settingsContainer.Show()
}

func (fyneUI *Fyne) buildSettings() {
	globalSettingsLabel := widget.NewLabel("Global Settings")
	globalSettingsLabel.TextStyle = fyne.TextStyle{Bold: true}

	groupSettingsLabel := widget.NewLabel("New Group Defaults")
	groupSettingsLabel.TextStyle = fyne.TextStyle{Bold: true}

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.mobileBack()
		} else {
			fyneUI.showMainContainer()
		}
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	fyneUI.settingsWidgets = &settingsWidgets{
		sendReadReceiptsByDefault:             widget.NewCheck("Send Read Receipts By Default", func(set bool) {}),
		sendTypingIndicatorsByDefault:         widget.NewCheck("Send Typing Indicators By Default", func(set bool) {}),
		defaultNewGroupRetention:              widget.NewSelect([]string{}, nil),
		defaultNewGroupRestrictUserManagement: widget.NewCheck("Restrict User Management", func(set bool) {}),
		defaultNewGroupRestrictGroupEdits:     widget.NewCheck("Restrict Group Edits", func(set bool) {}),
		defaultNewGroupRestrictPosting:        widget.NewCheck("Restrict Posting", func(set bool) {}),
	}

	fyneUI.settingsContainer = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.NewVScroll(container.NewVBox(
			container.NewCenter(makeLogo(228, 167)), // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
			globalSettingsLabel,
			fyneUI.settingsWidgets.sendReadReceiptsByDefault,
			fyneUI.settingsWidgets.sendTypingIndicatorsByDefault,
			groupSettingsLabel,
			widget.NewLabel("Retention"),
			fyneUI.settingsWidgets.defaultNewGroupRestrictUserManagement,
			fyneUI.settingsWidgets.defaultNewGroupRestrictGroupEdits,
			fyneUI.settingsWidgets.defaultNewGroupRestrictPosting,
		)),
	)
}

/*
func (fyneUI *Fyne) DefaultReadReceiptSettingSet(value bool) {
	fyneUI.settings.DefaultSendReadReceipts = value
	// update the check
}

func (fyneUI *Fyne) DefaultTypingIndicatorSettingSet(value bool) {
}
*/
