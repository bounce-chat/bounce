package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

var defaultOn = "Default (On)"
var defaultOff = "Default (Off)"
var on = "On"
var off = "Off"

type settingsWidgets struct {
	sendReadReceiptsByDefault             *widget.Check
	sendTypingIndicatorsByDefault         *widget.Check
	defaultNewGroupRetention              *widget.Select
	defaultNewGroupRestrictUserManagement *widget.Check
	defaultNewGroupRestrictGroupEdits     *widget.Check
	defaultNewGroupRestrictPosting        *widget.Check
	darkMode                              *widget.Check
}

func (ui *ui) showSettings() {
	ui.settingsWidgets.sendReadReceiptsByDefault.Checked = ui.settings.DefaultSendReadReceipts
	ui.settingsWidgets.sendReadReceiptsByDefault.Refresh()
	ui.settingsWidgets.sendTypingIndicatorsByDefault.Checked = ui.settings.DefaultSendTypingIndicators
	ui.settingsWidgets.sendTypingIndicatorsByDefault.Refresh()
	ui.settingsWidgets.defaultNewGroupRetention.Selected = getRetentionName(ui.settings.DefaultGroupRetention)
	ui.settingsWidgets.defaultNewGroupRetention.Refresh()
	ui.settingsWidgets.defaultNewGroupRestrictUserManagement.Checked = ui.settings.NewGroupRestrictUserManagement
	ui.settingsWidgets.defaultNewGroupRestrictUserManagement.Refresh()
	ui.settingsWidgets.defaultNewGroupRestrictGroupEdits.Checked = ui.settings.NewGroupRestrictGroupEdits
	ui.settingsWidgets.defaultNewGroupRestrictGroupEdits.Refresh()
	ui.settingsWidgets.defaultNewGroupRestrictPosting.Checked = ui.settings.NewGroupRestrictPosting
	ui.settingsWidgets.defaultNewGroupRestrictPosting.Refresh()
	ui.settingsWidgets.darkMode.Checked = ui.localSettings.DarkMode
	ui.settingsWidgets.darkMode.Refresh()

	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeSettings})
	}
	ui.mainWindow.SetContent(ui.settingsContainer)
	ui.settingsContainer.Show()
}

func (ui *ui) buildSettings() {
	globalSettingsLabel := widget.NewLabel("Global Settings")
	globalSettingsLabel.TextStyle = fyne.TextStyle{Bold: true}

	groupSettingsLabel := widget.NewLabel("New Group Defaults")
	groupSettingsLabel.TextStyle = fyne.TextStyle{Bold: true}

	themeSettingsLabel := widget.NewLabel("Theme")
	themeSettingsLabel.TextStyle = fyne.TextStyle{Bold: true}

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
		}
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	ui.settingsWidgets = &settingsWidgets{
		sendReadReceiptsByDefault:             widget.NewCheck("Send Read Receipts By Default", ui.bounce.SetReadReceiptsByDefault),
		sendTypingIndicatorsByDefault:         widget.NewCheck("Send Typing Indicators By Default", ui.bounce.SetTypingIndicatorsByDefault),
		defaultNewGroupRetention:              widget.NewSelect(retentionSelections, ui.sendDefaultRetentionSelection),
		defaultNewGroupRestrictUserManagement: widget.NewCheck("Restrict User Management", ui.bounce.SetNewGroupRestrictUserManagement),
		defaultNewGroupRestrictGroupEdits:     widget.NewCheck("Restrict Group Edits", ui.bounce.SetNewGroupRestrictGroupEdits),
		defaultNewGroupRestrictPosting:        widget.NewCheck("Restrict Posting", ui.bounce.SetNewGroupRestrictPosting),
		darkMode:                              widget.NewCheck("Dark Mode", ui.bounce.SetDarkMode),
	}

	ui.settingsContainer = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.NewVScroll(container.NewVBox(
			container.NewCenter(makeLogo(247, 75)),
			globalSettingsLabel,
			ui.settingsWidgets.sendReadReceiptsByDefault,
			ui.settingsWidgets.sendTypingIndicatorsByDefault,
			groupSettingsLabel,
			container.NewHBox(
				widget.NewLabel("Retention"),
				ui.settingsWidgets.defaultNewGroupRetention,
			),
			ui.settingsWidgets.defaultNewGroupRestrictUserManagement,
			ui.settingsWidgets.defaultNewGroupRestrictGroupEdits,
			ui.settingsWidgets.defaultNewGroupRestrictPosting,
			themeSettingsLabel,
			ui.settingsWidgets.darkMode,
		)),
	)
}

func (ui *ui) sendDefaultRetentionSelection(selection string) { // TODO: rename to group, different setting for user
	value, ok := retentionValues[selection]
	if !ok {
		log.WithFields(log.Fields{
			"selection": selection,
		}).Error("unsupported retention selection")
		return
	}
	ui.bounce.SetNewGroupRetention(value)
}

func (ui *ui) SetSettings(settings chat.Settings) {
	fyne.DoAndWait(func() {
		updateReadReceipts := ui.settings.DefaultSendReadReceipts != settings.DefaultSendReadReceipts
		updateTypingIndicators := ui.settings.DefaultSendTypingIndicators != settings.DefaultSendTypingIndicators

		ui.settings = settings

		ui.settingsWidgets.defaultNewGroupRetention.Selected = getRetentionName(settings.DefaultGroupRetention)
		ui.settingsWidgets.defaultNewGroupRetention.Refresh()

		if updateReadReceipts {
			ui.settingsWidgets.sendReadReceiptsByDefault.Checked = settings.DefaultSendReadReceipts
			ui.settingsWidgets.sendReadReceiptsByDefault.Refresh()
			for _, g := range ui.groups {
				g.refreshReadReceiptSettingSelection(ui.readReceiptOverrideSelectionOptions())
			}
			for _, dm := range ui.dms {
				dm.refreshReadReceiptSettingSelection(ui.readReceiptOverrideSelectionOptions())
			}
		}

		if updateTypingIndicators {
			ui.settingsWidgets.sendTypingIndicatorsByDefault.Checked = settings.DefaultSendTypingIndicators
			ui.settingsWidgets.sendTypingIndicatorsByDefault.Refresh()
			for _, g := range ui.groups {
				g.refreshTypingIndicatorSettingSelection(ui.typingIndicatorOverrideSelectionOptions())
			}
			for _, dm := range ui.dms {
				dm.refreshTypingIndicatorSettingSelection(ui.typingIndicatorOverrideSelectionOptions())
			}
		}

		ui.settingsWidgets.defaultNewGroupRestrictUserManagement.Checked = settings.NewGroupRestrictUserManagement
		ui.settingsWidgets.defaultNewGroupRestrictUserManagement.Refresh()

		ui.settingsWidgets.defaultNewGroupRestrictGroupEdits.Checked = settings.NewGroupRestrictGroupEdits
		ui.settingsWidgets.defaultNewGroupRestrictGroupEdits.Refresh()

		ui.settingsWidgets.defaultNewGroupRestrictPosting.Checked = settings.NewGroupRestrictPosting
		ui.settingsWidgets.defaultNewGroupRestrictPosting.Refresh()
	})
}

func (ui *ui) SetDarkMode(value bool) {
	fyne.DoAndWait(func() {
		if value {
			ui.app.Settings().SetTheme(&forcedVariant{Theme: theme.DefaultTheme(), variant: theme.VariantDark})
		} else {
			ui.app.Settings().SetTheme(&forcedVariant{Theme: theme.DefaultTheme(), variant: theme.VariantLight})
		}
		ui.localSettings.DarkMode = value
		ui.settingsWidgets.darkMode.Checked = value
		ui.settingsWidgets.darkMode.Refresh()
	})
}

func (ui *ui) readReceiptOverrideSelectionOptions() []string {
	strings := []string{}
	if ui.settings.DefaultSendReadReceipts {
		strings = append(strings, defaultOn)
	} else {
		strings = append(strings, defaultOff)
	}
	strings = append(strings, []string{on, off}...)
	return strings
}

func (ui *ui) typingIndicatorOverrideSelectionOptions() []string {
	strings := []string{}
	if ui.settings.DefaultSendTypingIndicators {
		strings = append(strings, defaultOn)
	} else {
		strings = append(strings, defaultOff)
	}
	strings = append(strings, []string{on, off}...)
	return strings
}
