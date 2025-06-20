package ui

import (
	"errors"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	log "github.com/sirupsen/logrus"
)

func (ui *ui) showEditThreadContainer(g *group) {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeGroupSettings, context: g.id})
	}
	ui.mainWindow.SetContent(g.editContainer)
	g.editContainer.Show()
}

func (ui *ui) buildEditThreadContainer(g *group) {
	currentThreadName, err := g.name.Get()
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	g.editThreadNameEntry.Text = currentThreadName

	g.name.AddListener(binding.NewDataListener(func() {
		newName, err := g.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		g.editThreadNameEntry.Text = newName
		g.editThreadNameEntry.Refresh()
	}))

	g.editIcon = newDefaultImage(g.id, g.images, g.initial, 128, ui.bounce.GetFileData, func() {
		dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if reader == nil {
				return
			}

			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Debug("error selecting new group image")
				return
			}

			var size int64
			size, reader, err = fileSizeInReader(reader)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error getting file size")
				reader.Close()
				return
			}
			if size > chat.EmbeddedFileLimit {
				ui.showDialog(dialog.NewError(errors.New("file is too large"), ui.mainWindow), nil)
				reader.Close()
				return
			}

			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Debug("error reading new group image")
				return
			}

			// TODO: make sure data is a valid image, allow for editing, etc
			// TODO: don't actualy set it until they hit save?

			ui.bounce.SetGroupImage(g.id, data)
		}, ui.mainWindow).Show() // We do not use showDialog here because on mobile this uses a native intent
	})

	g.retentionSelection = widget.NewSelect(retentionSelections, nil)
	g.retentionSelection.Selected = getRetentionName(g.retention)

	g.readReceiptOverrideSelection = widget.NewSelect(ui.readReceiptOverrideSelectionOptions(), nil)
	g.refreshReadReceiptSettingSelection(ui.readReceiptOverrideSelectionOptions())
	g.typingIndicatorOverrideSelection = widget.NewSelect(ui.typingIndicatorOverrideSelectionOptions(), nil)
	g.refreshTypingIndicatorSettingSelection(ui.readReceiptOverrideSelectionOptions())

	returnToThread := false
	var showError error
	confirmCleanup := func() {
		if returnToThread {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
			ui.mainWindow.Canvas().Focus(g.getEntry())
		}
		if showError != nil {
			ui.showDialog(dialog.NewError(showError, ui.mainWindow), nil)
		}
	}

	confirmClearHistory := dialog.NewConfirm(
		"Clear all message history?",
		"Are you sure you want to permanently delete all chat history on all devices?",
		func(confirmed bool) {
			returnToThread = confirmed
			showError = nil
			if confirmed {
				err := ui.bounce.ClearGroupChatHistory(g.id)
				if err != nil {
					showError = errors.New("error clearing chat history: " + err.Error())
					returnToThread = false
				}
			}
		},
		ui.mainWindow,
	)
	g.clearHistoryButton = widget.NewButton("Clear History", func() {
		ui.showDialog(confirmClearHistory, confirmCleanup)
	})

	confirmLeaveGroup := dialog.NewConfirm(
		"Leave Group?",
		"Are you sure you want to leave this group?",
		func(confirmed bool) {
			returnToThread = confirmed
			showError = nil
			if confirmed {
				err := ui.bounce.RemoveUserFromGroup(g.id, ui.profile.id)
				if err != nil {
					showError = errors.New("error leaving group: " + err.Error())
					returnToThread = false
				}
			}
		},
		ui.mainWindow,
	)

	g.leaveGroupButton = widget.NewButton("Leave Group", func() {
		ui.showDialog(confirmLeaveGroup, confirmCleanup)
	})

	confirmDeleteGroup := dialog.NewConfirm(
		"Delete this group?",
		"Are you sure you want to permanently delete this group for all members?",
		func(confirmed bool) {
			returnToThread = confirmed
			showError = nil
			if confirmed {
				err := ui.bounce.DeleteGroup(g.id)
				if err != nil {
					showError = errors.New("error deleting group: " + err.Error())
					returnToThread = false
				}
			}
		},
		ui.mainWindow,
	)
	g.deleteGroupButton = widget.NewButton("Delete Group", func() {
		ui.showDialog(confirmDeleteGroup, confirmCleanup)
	})

	confirmBlockGroup := dialog.NewConfirm(
		"Block this group?",
		"Are you sure you want to block this group?  You will never be able to join this group again.",
		func(confirmed bool) {
			returnToThread = confirmed
			showError = nil
			if confirmed {
				err := ui.bounce.BlockGroup(g.id)
				if err != nil {
					showError = errors.New("error blocking group: " + err.Error())
					returnToThread = false
				}
			}
		},
		ui.mainWindow,
	)
	g.blockGroupButton = widget.NewButton("Block Group", func() {
		ui.showDialog(confirmBlockGroup, confirmCleanup)
	})

	saveButton := widget.NewButton("Save", func() {
		// Update the g name if it was changed
		currentThreadName, err := g.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		newThreadName := g.editThreadNameEntry.Text
		if currentThreadName != newThreadName {
			err := ui.bounce.RenameGroup(g.id, strings.TrimSpace(newThreadName))
			if err != nil {
				ui.showDialog(dialog.NewError(errors.New("error renaming group: "+err.Error()), ui.mainWindow), nil)
				return
			}
		}

		// Update the notification settings if they have changes
		if g.notificationsEnabledCheck.Checked != (g.notificationsMutedUntil != chat.MutedForever) {
			mutedUntil := int64(0)
			if !g.notificationsEnabledCheck.Checked {
				mutedUntil = chat.MutedForever
			}
			err := ui.bounce.SetGroupMutedUntil(g.id, mutedUntil)
			if err != nil {
				ui.showDialog(dialog.NewError(errors.New("error updating group mute settings: "+err.Error()), ui.mainWindow), nil)
				return
			}
		}

		// Update the retention if it changed
		selectedRetentionString := g.retentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			dialog.ShowError(errors.New("invalid retention selection: "+selectedRetentionString), ui.mainWindow)
		} else {
			if g.retention != selectedRetentionValue {
				err = ui.bounce.SetGroupRetention(g.id, selectedRetentionValue)
				if err != nil {
					ui.showDialog(dialog.NewError(errors.New("error setting new retention value: "+err.Error()), ui.mainWindow), nil)
					return
				}
			}
		}

		// Update the permissions if needed
		if g.restrictUserManagementCheck.Checked && !g.restrictUserManagement {
			err := ui.bounce.RestrictUserManagement(g.id)
			if err != nil {
				ui.showDialog(dialog.NewError(errors.New("error restricting user management: "+err.Error()), ui.mainWindow), nil)
				return
			}
		}
		if !g.restrictUserManagementCheck.Checked && g.restrictUserManagement {
			err := ui.bounce.UnrestrictUserManagement(g.id)
			if err != nil {
				ui.showDialog(dialog.NewError(errors.New("error unrestricting user management: "+err.Error()), ui.mainWindow), nil)
				return
			}
		}
		if g.restrictGroupEditsCheck.Checked && !g.restrictGroupEdits {
			err := ui.bounce.RestrictGroupEdits(g.id)
			if err != nil {
				ui.showDialog(dialog.NewError(errors.New("error restricting group edits: "+err.Error()), ui.mainWindow), nil)
				return
			}
		}
		if !g.restrictGroupEditsCheck.Checked && g.restrictGroupEdits {
			err := ui.bounce.UnrestrictGroupEdits(g.id)
			if err != nil {
				ui.showDialog(dialog.NewError(errors.New("error unrestricting group edits: "+err.Error()), ui.mainWindow), nil)
				return
			}
		}
		if g.restrictPostingCheck.Checked && !g.restrictPosting {
			err := ui.bounce.RestrictPosting(g.id)
			if err != nil {
				ui.showDialog(dialog.NewError(errors.New("error restricting posting: "+err.Error()), ui.mainWindow), nil)
				return
			}
		}
		if !g.restrictPostingCheck.Checked && g.restrictPosting {
			err := ui.bounce.UnrestrictPosting(g.id)
			if err != nil {
				ui.showDialog(dialog.NewError(errors.New("error unrestricting posting: "+err.Error()), ui.mainWindow), nil)
				return
			}
		}

		// Capture read receipt selection state
		readReceiptSelection := g.readReceiptOverrideSelection.Selected
		defaultReadReceiptSelected := readReceiptSelection == defaultOn || readReceiptSelection == defaultOff
		switchedReadReceiptDefault := (defaultReadReceiptSelected && g.overrideReadReceiptSetting) || (!defaultReadReceiptSelected && !g.overrideReadReceiptSetting)
		switchedReadReceiptEnabled := (readReceiptSelection == off && g.readReceiptsEnabled) || (readReceiptSelection == on && !g.readReceiptsEnabled)
		// Capture typing indicator selection state
		typingIndicatorSelection := g.typingIndicatorOverrideSelection.Selected
		defaultTypingIndicatorSelected := typingIndicatorSelection == defaultOn || typingIndicatorSelection == defaultOff
		switchedTypingIndicatorDefault := (defaultTypingIndicatorSelected && g.overrideTypingIndicatorSetting) || (!defaultTypingIndicatorSelected && !g.overrideTypingIndicatorSetting)
		switchedTypingIndicatorEnabled := (typingIndicatorSelection == off && g.typingIndicatorsEnabled) || (typingIndicatorSelection == on && !g.typingIndicatorsEnabled)
		// Set values if needed
		if switchedReadReceiptDefault || switchedReadReceiptEnabled {
			if defaultReadReceiptSelected {
				ui.bounce.SetGroupReadReceiptSettings(g.id, false, true)
			} else if readReceiptSelection == on {
				ui.bounce.SetGroupReadReceiptSettings(g.id, true, true)
			} else if readReceiptSelection == off {
				ui.bounce.SetGroupReadReceiptSettings(g.id, true, false)
			} else {
				log.WithFields(log.Fields{
					"group_id":  g.id,
					"selection": readReceiptSelection,
				}).Error("invalid selection for read receipt override")
			}
		}
		if switchedTypingIndicatorDefault || switchedTypingIndicatorEnabled {
			if defaultTypingIndicatorSelected {
				ui.bounce.SetGroupTypingIndicatorSettings(g.id, false, true)
			} else if typingIndicatorSelection == on {
				ui.bounce.SetGroupTypingIndicatorSettings(g.id, true, true)
			} else if typingIndicatorSelection == off {
				ui.bounce.SetGroupTypingIndicatorSettings(g.id, true, false)
			} else {
				log.WithFields(log.Fields{
					"group_id":  g.id,
					"selection": typingIndicatorSelection,
				}).Error("invalid selection for typing indicator override")
			}
		}

		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
			ui.mainWindow.Canvas().Focus(g.getEntry())
		}
	})
	saveButton.Importance = widget.HighImportance

	cancelChanges := func() {
		// Reset name
		currentThreadName, err := g.name.Get()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}
		g.editThreadNameEntry.Text = currentThreadName
		g.editThreadNameEntry.Refresh()

		// Reset retention
		g.retentionSelection.Selected = getRetentionName(g.retention)
		g.retentionSelection.Refresh()

		// Reset notification settings
		enabled := g.notificationsMutedUntil != chat.MutedForever
		g.notificationsEnabledCheck.SetChecked(enabled)

		// Reset user editing
		g.pendingUsers.empty()
		ui.refreshUserSelections(g)

		// Reset permissions
		g.restrictUserManagementCheck.SetChecked(g.restrictUserManagement)
		g.restrictGroupEditsCheck.SetChecked(g.restrictGroupEdits)
		g.restrictPostingCheck.SetChecked(g.restrictPosting)

		// Reset the read receipt and typing indicator overrides
		g.refreshReadReceiptSettingSelection(ui.readReceiptOverrideSelectionOptions())
		g.refreshTypingIndicatorSettingSelection(ui.readReceiptOverrideSelectionOptions())

		// Show main tontainer
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
			ui.mainWindow.Canvas().Focus(g.getEntry())
		}
	}
	cancelButton := widget.NewButton("Cancel", cancelChanges)

	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, saveButton),
		saveButton,
		cancelButton,
	)

	usersLabel := widget.NewLabel("Users:")
	currentUsersListView := container.New(
		layout.NewBorderLayout(usersLabel, nil, nil, nil),
		usersLabel,
		g.currentUsersContainer,
	)

	g.newUserSearchEntry = widget.NewEntry()
	g.newUserSearchEntry.OnChanged = func(str string) {
		ui.refreshAvailableNewUsers(g, ui.users.search(str))
	}
	newUserSelector := container.New(
		layout.NewBorderLayout(g.newUserSearchEntry, nil, nil, nil),
		g.newUserSearchEntry,
		container.NewVBox(
			widget.NewLabel("Users to add:"),
			g.pendingUsersContainer,
			widget.NewLabel("All Users:"),
			g.availableNewUsersScroll,
		),
	)

	addUsersDialogCleanup := func() {
		g.pendingUsers.empty()
		ui.refreshUserSelections(g)
		g.newUserSearchEntry.Text = ""
		g.newUserSearchEntry.Refresh()
		ui.refreshAvailableNewUsers(g, ui.users.search(g.newUserSearchEntry.Text))

		if showError != nil {
			ui.showDialog(dialog.NewError(showError, ui.mainWindow), nil)
		}
	}
	addUsersDialog := dialog.NewCustomConfirm("Add Users", "Save", "Cancel", newUserSelector, func(apply bool) {
		showError = nil
		if apply {
			for _, thisUser := range g.pendingUsers.userList {
				err := ui.bounce.AddUserToGroup(g.id, thisUser.id)
				if err != nil {
					showError = errors.New("error adding user: " + err.Error())
					return
				}
			}
		}
	}, ui.mainWindow)
	g.addUsersButton = widget.NewButton("Add Users", func() {
		ui.showDialog(
			addUsersDialog,
			addUsersDialogCleanup,
		)
	})

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), cancelChanges)
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	editGroupFeatures := container.NewVBox(
		container.NewCenter(g.editIcon),
		g.editThreadNameEntry,
		g.notificationsEnabledCheck,
		widget.NewLabel("Disappearing Messages"),
		g.retentionSelection,
		container.NewHBox(
			container.NewHBox(g.clearHistoryButton),
			container.NewHBox(g.leaveGroupButton),
			container.NewHBox(g.deleteGroupButton),
			container.NewHBox(g.blockGroupButton),
		),
		g.restrictUserManagementCheck,
		g.restrictGroupEditsCheck,
		g.restrictPostingCheck,
		currentUsersListView,
		container.NewHBox(g.addUsersButton),
		widget.NewAccordion(
			&widget.AccordionItem{
				Title: "Advanced Options",
				Detail: container.NewVBox(
					widget.NewLabel("Read Receipts"),
					g.readReceiptOverrideSelection,
					widget.NewLabel("Typing Indicators"),
					g.typingIndicatorOverrideSelection,
				),
			},
		),
	)

	g.editContainer = container.NewMax(
		container.New(
			layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
			container.NewVScroll(editGroupFeatures),
			closeBar,
			actionButtons,
		),
	)
}

func (ui *ui) refreshCurrentAndPendingUsers(g *group) {
	currentUsersList := container.NewVBox()
	adminMap := map[uuid.UUID]bool{}
	for _, adminID := range g.admins {
		adminMap[adminID] = true
	}
	adminSorted := []*user{}
	for _, thisUser := range g.users.alphabetized() {
		func(u *user) {
			if _, present := adminMap[u.id]; present {
				adminSorted = append(adminSorted, u)
			}
		}(thisUser)
	}
	for _, thisUser := range g.users.alphabetized() {
		func(u *user) {
			if _, present := adminMap[u.id]; !present {
				adminSorted = append(adminSorted, u)
			}
		}(thisUser)
	}

	for _, thisUser := range adminSorted {
		func(u *user) {
			// TODO: setup listener to update the button text below
			userDetailsButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, ui.bounce.GetFileData, nil),
				u.getName(),
				g.isAdmin(u.id),
				func() {
					d, callback := ui.getEditUserDialog(g, u.id)
					ui.showDialog(d, callback)
				},
			)

			currentUsersList.Objects = append(
				currentUsersList.Objects,
				userDetailsButton,
			)
		}(thisUser)
	}
	g.currentUsersContainer.Content = currentUsersList
	currentUserHeight := float32(0)
	for i, obj := range currentUsersList.Objects {
		if i == 6 {
			break
		}
		currentUserHeight += obj.MinSize().Height
	}
	currentUserHeight += theme.Padding() * float32(len(currentUsersList.Objects)+1)
	g.currentUsersContainer.SetMinSize(fyne.Size{Height: currentUserHeight})
	g.currentUsersContainer.Refresh()

	pendingUsersList := container.NewVBox()
	for _, thisUser := range g.pendingUsers.alphabetized() {
		func(u *user) {
			// TODO: setup listener to update the button text below
			removePendingUserButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize(), ui.bounce.GetFileData, nil),
				u.getName(),
				false,
				func() {
					g.pendingUsers.remove(u.id)
					ui.refreshUserSelections(g)
				},
			)
			pendingUsersList.Objects = append(
				pendingUsersList.Objects,
				removePendingUserButton,
			)
		}(thisUser)
	}
	g.pendingUsersContainer.Content = pendingUsersList
	pendingUserHeight := float32(0)
	for i, obj := range pendingUsersList.Objects {
		if i == 3 {
			break
		}
		pendingUserHeight += obj.MinSize().Height
	}
	pendingUserHeight += theme.Padding() * float32(len(pendingUsersList.Objects)+1)
	g.pendingUsersContainer.SetMinSize(fyne.Size{Height: pendingUserHeight})
	g.pendingUsersContainer.Refresh()
}

func (ui *ui) refreshAvailableNewUsers(g *group, allAvailableUsers []*user) {
	blockedUsersMap := map[uuid.UUID]bool{}
	for _, id := range g.blockedUsers {
		blockedUsersMap[id] = true
	}

	allUsersListBox := container.NewVBox()
	for _, thisUser := range allAvailableUsers {
		// Exclude users already in the group
		if _, exists := g.users.get(thisUser.id); exists {
			continue
		}
		// Exclude users that are pending addition to the group
		if _, exists := g.pendingUsers.get(thisUser.id); exists {
			continue
		}

		// Hide blocked users
		if _, exists := blockedUsersMap[thisUser.id]; exists {
			continue
		}

		func(u *user) {
			// TODO: setup listener to update the button text below
			addUserButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize(), ui.bounce.GetFileData, nil),
				u.getName(),
				false,
				func() {
					g.pendingUsers.add(u)
					ui.refreshUserSelections(g)
				},
			)
			allUsersListBox.Objects = append(
				allUsersListBox.Objects,
				addUserButton,
			)
		}(thisUser)
	}
	g.availableNewUsersScroll.Content = allUsersListBox
	availableUserHeight := float32(0)
	for i, obj := range allUsersListBox.Objects {
		if i == 3 {
			break
		}
		availableUserHeight += obj.MinSize().Height
	}
	availableUserHeight += theme.Padding() * float32(len(allUsersListBox.Objects)+1)
	g.availableNewUsersScroll.SetMinSize(fyne.Size{Height: availableUserHeight})
	g.availableNewUsersScroll.Refresh()
}

func (ui *ui) refreshUserSelections(g *group) {
	ui.refreshCurrentAndPendingUsers(g)
	ui.refreshAvailableNewUsers(g, ui.users.search(g.newUserSearchEntry.Text))
}
