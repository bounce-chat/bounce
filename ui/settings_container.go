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

type settings struct {
	sendReadReceiptsByDefault             *widget.Check
	sendTypingIndicatorsByDefault         *widget.Check
	defaultNewGroupRetention              *widget.Select
	defaultNewGroupRestrictUserManagement *widget.Check
	defaultNewGroupRestrictGroupEdits     *widget.Check
	defaultNewGroupRestrictPosting        *widget.Check
	darkMode                              *widget.Check
}

func (ui *ui) showSettings() {
	ui.widgets.settings.sendReadReceiptsByDefault.Checked = ui.state.settings.DefaultSendReadReceipts
	ui.widgets.settings.sendReadReceiptsByDefault.Refresh()
	ui.widgets.settings.sendTypingIndicatorsByDefault.Checked = ui.state.settings.DefaultSendTypingIndicators
	ui.widgets.settings.sendTypingIndicatorsByDefault.Refresh()
	ui.widgets.settings.defaultNewGroupRetention.Selected = getRetentionName(ui.state.settings.DefaultGroupRetention)
	ui.widgets.settings.defaultNewGroupRetention.Refresh()
	ui.widgets.settings.defaultNewGroupRestrictUserManagement.Checked = ui.state.settings.NewGroupRestrictUserManagement
	ui.widgets.settings.defaultNewGroupRestrictUserManagement.Refresh()
	ui.widgets.settings.defaultNewGroupRestrictGroupEdits.Checked = ui.state.settings.NewGroupRestrictGroupEdits
	ui.widgets.settings.defaultNewGroupRestrictGroupEdits.Refresh()
	ui.widgets.settings.defaultNewGroupRestrictPosting.Checked = ui.state.settings.NewGroupRestrictPosting
	ui.widgets.settings.defaultNewGroupRestrictPosting.Refresh()
	ui.widgets.settings.darkMode.Checked = ui.app.Preferences().Bool(darkMode)
	ui.widgets.settings.darkMode.Refresh()

	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeSettings})
	}
	ui.state.currentView = viewTypeSettings
	ui.window.SetContent(ui.views.settings)
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

	ui.widgets.settings = &settings{
		sendReadReceiptsByDefault:             widget.NewCheck("Send Read Receipts By Default", ui.bounce.SetReadReceiptsByDefault),
		sendTypingIndicatorsByDefault:         widget.NewCheck("Send Typing Indicators By Default", ui.bounce.SetTypingIndicatorsByDefault),
		defaultNewGroupRetention:              widget.NewSelect(retentionSelections, ui.sendDefaultRetentionSelection),
		defaultNewGroupRestrictUserManagement: widget.NewCheck("Restrict User Management", ui.bounce.SetNewGroupRestrictUserManagement),
		defaultNewGroupRestrictGroupEdits:     widget.NewCheck("Restrict Group Edits", ui.bounce.SetNewGroupRestrictGroupEdits),
		defaultNewGroupRestrictPosting:        widget.NewCheck("Restrict Posting", ui.bounce.SetNewGroupRestrictPosting),
		darkMode:                              widget.NewCheck("Dark Mode", ui.setDarkMode),
	}

	ui.views.settings = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.NewVScroll(container.NewVBox(
			container.NewCenter(makeLogo(247, 75)),
			globalSettingsLabel,
			ui.widgets.settings.sendReadReceiptsByDefault,
			ui.widgets.settings.sendTypingIndicatorsByDefault,
			groupSettingsLabel,
			container.NewHBox(
				widget.NewLabel("Retention"),
				ui.widgets.settings.defaultNewGroupRetention,
			),
			ui.widgets.settings.defaultNewGroupRestrictUserManagement,
			ui.widgets.settings.defaultNewGroupRestrictGroupEdits,
			ui.widgets.settings.defaultNewGroupRestrictPosting,
			themeSettingsLabel,
			ui.widgets.settings.darkMode,
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
		updateReadReceipts := ui.state.settings.DefaultSendReadReceipts != settings.DefaultSendReadReceipts
		updateTypingIndicators := ui.state.settings.DefaultSendTypingIndicators != settings.DefaultSendTypingIndicators

		ui.state.settings = settings

		ui.widgets.settings.defaultNewGroupRetention.Selected = getRetentionName(settings.DefaultGroupRetention)
		ui.widgets.settings.defaultNewGroupRetention.Refresh()

		if updateReadReceipts {
			ui.widgets.settings.sendReadReceiptsByDefault.Checked = settings.DefaultSendReadReceipts
			ui.widgets.settings.sendReadReceiptsByDefault.Refresh()
			ui.threads.rangeFunc(func(t thread) {
				t.refreshReadReceiptSettingSelection(ui.readReceiptOverrideSelectionOptions())
			})
		}

		if updateTypingIndicators {
			ui.widgets.settings.sendTypingIndicatorsByDefault.Checked = settings.DefaultSendTypingIndicators
			ui.widgets.settings.sendTypingIndicatorsByDefault.Refresh()
			ui.threads.rangeFunc(func(t thread) {
				t.refreshTypingIndicatorSettingSelection(ui.typingIndicatorOverrideSelectionOptions())
			})
		}

		ui.widgets.settings.defaultNewGroupRestrictUserManagement.Checked = settings.NewGroupRestrictUserManagement
		ui.widgets.settings.defaultNewGroupRestrictUserManagement.Refresh()

		ui.widgets.settings.defaultNewGroupRestrictGroupEdits.Checked = settings.NewGroupRestrictGroupEdits
		ui.widgets.settings.defaultNewGroupRestrictGroupEdits.Refresh()

		ui.widgets.settings.defaultNewGroupRestrictPosting.Checked = settings.NewGroupRestrictPosting
		ui.widgets.settings.defaultNewGroupRestrictPosting.Refresh()
	})
}

func (ui *ui) setDarkMode(value bool) {
	fyne.DoAndWait(func() {
		ui.app.Preferences().SetBool(darkMode, value)
		ui.widgets.settings.darkMode.Checked = value
		ui.widgets.settings.darkMode.Refresh()
		if value {
			ui.app.Settings().SetTheme(&forcedVariant{Theme: theme.DefaultTheme(), variant: theme.VariantDark})
		} else {
			ui.app.Settings().SetTheme(&forcedVariant{Theme: theme.DefaultTheme(), variant: theme.VariantLight})
		}
	})
}

func (ui *ui) readReceiptOverrideSelectionOptions() []string {
	strings := []string{}
	if ui.state.settings.DefaultSendReadReceipts {
		strings = append(strings, defaultOn)
	} else {
		strings = append(strings, defaultOff)
	}
	strings = append(strings, []string{on, off}...)
	return strings
}

func (ui *ui) typingIndicatorOverrideSelectionOptions() []string {
	strings := []string{}
	if ui.state.settings.DefaultSendTypingIndicators {
		strings = append(strings, defaultOn)
	} else {
		strings = append(strings, defaultOff)
	}
	strings = append(strings, []string{on, off}...)
	return strings
}
