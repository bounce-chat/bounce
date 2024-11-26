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
	fyneUI.settingsWidgets.sendReadReceiptsByDefault.Checked = fyneUI.settings.DefaultSendReadReceipts
	fyneUI.settingsWidgets.sendReadReceiptsByDefault.Refresh()
	fyneUI.settingsWidgets.sendTypingIndicatorsByDefault.Checked = fyneUI.settings.DefaultSendTypingIndicators
	fyneUI.settingsWidgets.sendTypingIndicatorsByDefault.Refresh()
	fyneUI.settingsWidgets.defaultNewGroupRestrictUserManagement.Checked = fyneUI.settings.NewGroupRestrictUserManagement
	fyneUI.settingsWidgets.defaultNewGroupRestrictUserManagement.Refresh()
	fyneUI.settingsWidgets.defaultNewGroupRestrictGroupEdits.Checked = fyneUI.settings.NewGroupRestrictGroupEdits
	fyneUI.settingsWidgets.defaultNewGroupRestrictGroupEdits.Refresh()
	fyneUI.settingsWidgets.defaultNewGroupRestrictPosting.Checked = fyneUI.settings.NewGroupRestrictPosting
	fyneUI.settingsWidgets.defaultNewGroupRestrictPosting.Refresh()

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
		sendReadReceiptsByDefault:             widget.NewCheck("Send Read Receipts By Default", fyneUI.callbacks.SetReadReceiptsByDefault),
		sendTypingIndicatorsByDefault:         widget.NewCheck("Send Typing Indicators By Default", fyneUI.callbacks.SetTypingIndicatorsByDefault),
		defaultNewGroupRetention:              widget.NewSelect([]string{}, nil),
		defaultNewGroupRestrictUserManagement: widget.NewCheck("Restrict User Management", fyneUI.callbacks.SetNewGroupRestrictUserManagement),
		defaultNewGroupRestrictGroupEdits:     widget.NewCheck("Restrict Group Edits", fyneUI.callbacks.SetNewGroupRestrictGroupEdits),
		defaultNewGroupRestrictPosting:        widget.NewCheck("Restrict Posting", fyneUI.callbacks.SetNewGroupRestrictPosting),
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
			container.NewHBox(
				widget.NewLabel("Retention"),
				fyneUI.settingsWidgets.defaultNewGroupRetention,
			),
			fyneUI.settingsWidgets.defaultNewGroupRestrictUserManagement,
			fyneUI.settingsWidgets.defaultNewGroupRestrictGroupEdits,
			fyneUI.settingsWidgets.defaultNewGroupRestrictPosting,
		)),
	)
}

func (fyneUI *Fyne) DefaultReadReceiptSettingSet(value bool) {
	fyneUI.settings.DefaultSendReadReceipts = value
	fyneUI.settingsWidgets.sendReadReceiptsByDefault.Checked = value
	fyneUI.settingsWidgets.sendReadReceiptsByDefault.Refresh()
}

func (fyneUI *Fyne) DefaultTypingIndicatorSettingSet(value bool) {
	fyneUI.settings.DefaultSendTypingIndicators = value
	fyneUI.settingsWidgets.sendTypingIndicatorsByDefault.Checked = value
	fyneUI.settingsWidgets.sendTypingIndicatorsByDefault.Refresh()
}

func (fyneUI *Fyne) DefaultGroupRetentionSettingSet(value int64) {
	//fyneUI.settings. = value
	//fyneUI.settingsWidgets..Selected =
	//fyneUI.settingsWidgets..Refresh()
}

func (fyneUI *Fyne) NewGroupRestrictUserManagementSettingSet(value bool) {
	fyneUI.settings.NewGroupRestrictUserManagement = value
	fyneUI.settingsWidgets.defaultNewGroupRestrictUserManagement.Checked = value
	fyneUI.settingsWidgets.defaultNewGroupRestrictUserManagement.Refresh()
}

func (fyneUI *Fyne) NewGroupRestrictGroupEditsSettingSet(value bool) {
	fyneUI.settings.NewGroupRestrictGroupEdits = value
	fyneUI.settingsWidgets.defaultNewGroupRestrictGroupEdits.Checked = value
	fyneUI.settingsWidgets.defaultNewGroupRestrictGroupEdits.Refresh()
}

func (fyneUI *Fyne) NewGroupRestrictPostingSettingSet(value bool) {
	fyneUI.settings.NewGroupRestrictPosting = value
	fyneUI.settingsWidgets.defaultNewGroupRestrictPosting.Checked = value
	fyneUI.settingsWidgets.defaultNewGroupRestrictPosting.Refresh()
}
