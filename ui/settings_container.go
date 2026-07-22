package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/bounce-chat/bounce/chat"
	log "github.com/sirupsen/logrus"
)

var defaultOn = "Default (On)"
var defaultOff = "Default (Off)"
var on = "On"
var off = "Off"

var whenKnowEveryone = "When I know everyone in the group"
var never = "Never"
var always = "Always"
var autoAcceptGroupInviteSelections = []string{whenKnowEveryone, never, always}

var autoJoinGroupLabels = map[int]string{
	chat.OnlyAutoJoinGroupsWithNoNewUsers: whenKnowEveryone,
	chat.NeverAutoJoinGroups:              never,
	chat.AlwaysAutoJoinGroups:             always,
}

var autoJoinGroupValues = map[string]int{
	whenKnowEveryone: chat.OnlyAutoJoinGroupsWithNoNewUsers,
	never:            chat.NeverAutoJoinGroups,
	always:           chat.AlwaysAutoJoinGroups,
}

type settings struct {
	sendReadReceiptsByDefault             *widget.Check
	sendTypingIndicatorsByDefault         *widget.Check
	defaultNewDMRetention                 *widget.Select
	defaultNewGroupRetention              *widget.Select
	defaultNewGroupRestrictUserManagement *widget.Check
	defaultNewGroupRestrictGroupEdits     *widget.Check
	defaultNewGroupRestrictPosting        *widget.Check
	autoAcceptGroupInvites                *widget.Select
	darkMode                              *widget.Check
	sysTray                               *widget.Check
}

func (ui *ui) showSettings() {
	ui.widgets.settings.sendReadReceiptsByDefault.Checked = ui.state.settings.DefaultSendReadReceipts
	ui.widgets.settings.sendReadReceiptsByDefault.Refresh()
	ui.widgets.settings.sendTypingIndicatorsByDefault.Checked = ui.state.settings.DefaultSendTypingIndicators
	ui.widgets.settings.sendTypingIndicatorsByDefault.Refresh()
	ui.widgets.settings.defaultNewDMRetention.Selected = getRetentionName(ui.state.settings.DefaultDMRetention)
	ui.widgets.settings.defaultNewDMRetention.Refresh()

	ui.widgets.settings.autoAcceptGroupInvites.Selected = getAutoJoinLabel(ui.state.settings.AutoJoinGroups)
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
	ui.widgets.settings.sysTray.Checked = ui.app.Preferences().Bool(sysTray)
	ui.widgets.settings.sysTray.Refresh()

	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeSettings})
	}
	ui.setContent(viewTypeSettings, ui.views.settings)
}

func (ui *ui) buildSettings() {
	globalSettingsLabel := widget.NewLabel("Global Settings")
	globalSettingsLabel.TextStyle = fyne.TextStyle{Bold: true}

	groupSettingsLabel := widget.NewLabel("New Group Defaults")
	groupSettingsLabel.TextStyle = fyne.TextStyle{Bold: true}

	themeSettingsLabel := widget.NewLabel("Theme")
	themeSettingsLabel.TextStyle = fyne.TextStyle{Bold: true}

	var closeBar fyne.CanvasObject
	if fyne.CurrentDevice().IsMobile() {
		closeButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
		})
		closeButton.Importance = widget.LowImportance

		closeBar = container.New(
			layout.NewBorderLayout(nil, nil, closeButton, nil),
			closeButton,
		)
	} else {
		closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
		})
		closeButton.Importance = widget.LowImportance

		closeBar = container.New(
			layout.NewBorderLayout(nil, nil, nil, closeButton),
			closeButton,
		)
	}

	ui.widgets.settings = &settings{
		sendReadReceiptsByDefault:             widget.NewCheck("Send Read Receipts By Default", func(v bool) { ui.bounce.SetReadReceiptsByDefault(v) }),
		sendTypingIndicatorsByDefault:         widget.NewCheck("Send Typing Indicators By Default", func(v bool) { ui.bounce.SetTypingIndicatorsByDefault(v) }),
		defaultNewDMRetention:                 widget.NewSelect(retentionSelections, ui.sendDefaultDMRetentionSelection),
		defaultNewGroupRetention:              widget.NewSelect(retentionSelections, ui.sendDefaultGroupRetentionSelection),
		defaultNewGroupRestrictUserManagement: widget.NewCheck("Restrict User Management", func(v bool) { ui.bounce.SetNewGroupRestrictUserManagement(v) }),
		defaultNewGroupRestrictGroupEdits:     widget.NewCheck("Restrict Group Edits", func(v bool) { ui.bounce.SetNewGroupRestrictGroupEdits(v) }),
		defaultNewGroupRestrictPosting:        widget.NewCheck("Restrict Posting", func(v bool) { ui.bounce.SetNewGroupRestrictPosting(v) }),
		autoAcceptGroupInvites:                widget.NewSelect(autoAcceptGroupInviteSelections, ui.sendDefaultAutoJoinGroups),
		darkMode:                              widget.NewCheck("Dark Mode", ui.setDarkMode),
		sysTray:                               widget.NewCheck("Use System Tray (requires restart)", ui.setSysTray),
	}

	header := container.NewVBox(
		closeBar,
		makeLogo(247, 75),
	)

	allSettings := container.NewVBox(
		globalSettingsLabel,
		ui.widgets.settings.sendReadReceiptsByDefault,
		ui.widgets.settings.sendTypingIndicatorsByDefault,
		container.NewHBox(
			widget.NewLabel("Automatically Accept Group Invites"),
			ui.widgets.settings.autoAcceptGroupInvites,
		),
		container.NewHBox(
			widget.NewLabel("Default Direct Message Retention"),
			ui.widgets.settings.defaultNewDMRetention,
		),
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
	)
	if !fyne.CurrentDevice().IsMobile() {
		allSettings.Add(ui.widgets.settings.sysTray)
	}
	ui.views.settings = container.New(
		layout.NewBorderLayout(header, nil, nil, nil),
		header,
		container.NewVScroll(allSettings),
	)
}

func (ui *ui) sendDefaultGroupRetentionSelection(selection string) {
	value, ok := retentionValues[selection]
	if !ok {
		log.WithFields(log.Fields{
			"selection": selection,
		}).Error("unsupported retention selection")
		return
	}
	ui.bounce.SetNewGroupRetention(value)
}

func (ui *ui) sendDefaultDMRetentionSelection(selection string) {
	value, ok := retentionValues[selection]
	if !ok {
		log.WithFields(log.Fields{
			"selection": selection,
		}).Error("unsupported retention selection")
		return
	}
	ui.bounce.SetNewDMRetention(value)
}

func (ui *ui) sendDefaultAutoJoinGroups(selection string) {
	value, ok := autoJoinGroupValues[selection]
	if !ok {
		log.WithFields(log.Fields{
			"selection": selection,
		}).Error("unsupported auto join group selection")
		return
	}
	ui.bounce.SetAutoJoinGroups(value)
}

func getAutoJoinLabel(setting int) string {
	autoJoinLabel, ok := autoJoinGroupLabels[setting]
	if !ok {
		autoJoinLabel = "unsupported setting"
	}
	return autoJoinLabel
}

func (ui *ui) SetSettings(settings chat.Settings) {
	fyne.DoAndWait(func() {
		updateReadReceipts := ui.state.settings.DefaultSendReadReceipts != settings.DefaultSendReadReceipts
		updateTypingIndicators := ui.state.settings.DefaultSendTypingIndicators != settings.DefaultSendTypingIndicators

		ui.state.settings = settings

		ui.widgets.settings.defaultNewDMRetention.Selected = getRetentionName(settings.DefaultDMRetention)
		ui.widgets.settings.defaultNewDMRetention.Refresh()

		ui.widgets.settings.defaultNewGroupRetention.Selected = getRetentionName(settings.DefaultGroupRetention)
		ui.widgets.settings.defaultNewGroupRetention.Refresh()

		ui.widgets.settings.autoAcceptGroupInvites.Selected = getAutoJoinLabel(settings.AutoJoinGroups)
		ui.widgets.settings.autoAcceptGroupInvites.Refresh()

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
	ui.app.Preferences().SetBool(darkMode, value)
	ui.widgets.settings.darkMode.Checked = value
	ui.widgets.settings.darkMode.Refresh()
	if value {
		ui.app.Settings().SetTheme(&forcedVariant{Theme: theme.DefaultTheme(), variant: theme.VariantDark})
		setStatusBar(true)
	} else {
		ui.app.Settings().SetTheme(&forcedVariant{Theme: theme.DefaultTheme(), variant: theme.VariantLight})
		setStatusBar(false)
	}
	if fyne.CurrentDevice().IsMobile() {
		ui.views.main.Refresh()
		ui.views.newInstall.Refresh()
		ui.views.newSyncDevice.Refresh()
		ui.views.addSyncDevice.Refresh()
		ui.views.nameNewDevice.Refresh()
		ui.views.addUser.Refresh()
		ui.views.newProfileCreator.Refresh()
		ui.views.databaseLoading.Refresh()
		ui.views.editProfile.Refresh()
		ui.views.settings.Refresh()
		ui.views.about.Refresh()
		ui.views.newGroup.Refresh()
		ui.views.newDM.Refresh()
		ui.views.mobileMenu.Refresh()
		ui.containers.threads.Refresh()
	}
}

func (ui *ui) setSysTray(value bool) {
	ui.app.Preferences().SetBool(sysTray, value)
	ui.widgets.settings.sysTray.Checked = value
	ui.widgets.settings.sysTray.Refresh()
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
