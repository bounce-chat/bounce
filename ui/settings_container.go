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
}

func (fyneUI *Fyne) showSettings() {
	fyneUI.settingsWidgets.sendReadReceiptsByDefault.Checked = fyneUI.settings.DefaultSendReadReceipts
	fyneUI.settingsWidgets.sendReadReceiptsByDefault.Refresh()
	fyneUI.settingsWidgets.sendTypingIndicatorsByDefault.Checked = fyneUI.settings.DefaultSendTypingIndicators
	fyneUI.settingsWidgets.sendTypingIndicatorsByDefault.Refresh()
	fyneUI.settingsWidgets.defaultNewGroupRetention.Selected = getRetentionName(fyneUI.settings.DefaultGroupRetention)
	fyneUI.settingsWidgets.defaultNewGroupRetention.Refresh()
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
		defaultNewGroupRetention:              widget.NewSelect(retentionSelections, fyneUI.sendDefaultRetentionSelection),
		defaultNewGroupRestrictUserManagement: widget.NewCheck("Restrict User Management", fyneUI.callbacks.SetNewGroupRestrictUserManagement),
		defaultNewGroupRestrictGroupEdits:     widget.NewCheck("Restrict Group Edits", fyneUI.callbacks.SetNewGroupRestrictGroupEdits),
		defaultNewGroupRestrictPosting:        widget.NewCheck("Restrict Posting", fyneUI.callbacks.SetNewGroupRestrictPosting),
	}

	fyneUI.settingsContainer = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.NewVScroll(container.NewVBox(
			container.NewCenter(makeLogo(247, 75)),
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

func (fyneUI *Fyne) sendDefaultRetentionSelection(selection string) { // TODO: rename to group, different setting for user
	value, ok := retentionValues[selection]
	if !ok {
		log.WithFields(log.Fields{
			"selection": selection,
		}).Error("unsupported retention selection")
		return
	}
	fyneUI.callbacks.SetNewGroupRetention(value)
}

func (fyneUI *Fyne) SetSettings(settings chat.Settings) {
	fyneUI.settings.DefaultGroupRetention = settings.DefaultGroupRetention // TODO: can just set the whole struct when not mingling local settings into this struct
	fyneUI.settingsWidgets.defaultNewGroupRetention.Selected = getRetentionName(settings.DefaultGroupRetention)
	fyneUI.settingsWidgets.defaultNewGroupRetention.Refresh()

	updateReadReceipts := fyneUI.settings.DefaultSendReadReceipts != settings.DefaultSendReadReceipts
	if updateReadReceipts {
		fyneUI.settings.DefaultSendReadReceipts = settings.DefaultSendReadReceipts
		fyneUI.settingsWidgets.sendReadReceiptsByDefault.Checked = settings.DefaultSendReadReceipts
		fyneUI.settingsWidgets.sendReadReceiptsByDefault.Refresh()
		for _, g := range fyneUI.groups {
			g.refreshReadReceiptSettingSelection(fyneUI.readReceiptOverrideSelectionOptions())
		}
		for _, dm := range fyneUI.dms {
			dm.refreshReadReceiptSettingSelection(fyneUI.readReceiptOverrideSelectionOptions())
		}
	}

	updateTypingIndicators := fyneUI.settings.DefaultSendTypingIndicators != settings.DefaultSendTypingIndicators
	if updateTypingIndicators {
		fyneUI.settings.DefaultSendTypingIndicators = settings.DefaultSendTypingIndicators
		fyneUI.settingsWidgets.sendTypingIndicatorsByDefault.Checked = settings.DefaultSendTypingIndicators
		fyneUI.settingsWidgets.sendTypingIndicatorsByDefault.Refresh()
		for _, g := range fyneUI.groups {
			g.refreshTypingIndicatorSettingSelection(fyneUI.typingIndicatorOverrideSelectionOptions())
		}
		for _, dm := range fyneUI.dms {
			dm.refreshTypingIndicatorSettingSelection(fyneUI.typingIndicatorOverrideSelectionOptions())
		}
	}

	fyneUI.settings.NewGroupRestrictUserManagement = settings.NewGroupRestrictUserManagement
	fyneUI.settingsWidgets.defaultNewGroupRestrictUserManagement.Checked = settings.NewGroupRestrictUserManagement
	fyneUI.settingsWidgets.defaultNewGroupRestrictUserManagement.Refresh()

	fyneUI.settings.NewGroupRestrictGroupEdits = settings.NewGroupRestrictGroupEdits
	fyneUI.settingsWidgets.defaultNewGroupRestrictGroupEdits.Checked = settings.NewGroupRestrictGroupEdits
	fyneUI.settingsWidgets.defaultNewGroupRestrictGroupEdits.Refresh()

	fyneUI.settings.NewGroupRestrictPosting = settings.NewGroupRestrictPosting
	fyneUI.settingsWidgets.defaultNewGroupRestrictPosting.Checked = settings.NewGroupRestrictPosting
	fyneUI.settingsWidgets.defaultNewGroupRestrictPosting.Refresh()
}

func (fyneUI *Fyne) readReceiptOverrideSelectionOptions() []string {
	strings := []string{}
	if fyneUI.settings.DefaultSendReadReceipts {
		strings = append(strings, defaultOn)
	} else {
		strings = append(strings, defaultOff)
	}
	strings = append(strings, []string{on, off}...)
	return strings
}

func (fyneUI *Fyne) typingIndicatorOverrideSelectionOptions() []string {
	strings := []string{}
	if fyneUI.settings.DefaultSendTypingIndicators {
		strings = append(strings, defaultOn)
	} else {
		strings = append(strings, defaultOff)
	}
	strings = append(strings, []string{on, off}...)
	return strings
}
