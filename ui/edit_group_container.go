package ui

import (
	"errors"
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

func (fyneUI *Fyne) showEditThreadContainer(g *group) {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeGroupSettings, context: g.id})
	}
	fyneUI.mainWindow.SetContent(g.editContainer)
	g.editContainer.Show()
}

func (fyneUI *Fyne) buildEditThreadContainer(g *group) {
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

	groupIcon := newDefaultImage(g.id, g.initial, 128, func() {
		log.Info("open image replacement selector for group, if permissions allow")
	})

	g.retentionSelection = widget.NewSelect(retentionSelections, nil)
	g.retentionSelection.Selected = getRetentionName(g.retention)

	g.readReceiptOverrideSelection = widget.NewSelect(fyneUI.readReceiptOverrideSelectionOptions(), nil)
	g.refreshReadReceiptSettingSelection(fyneUI.readReceiptOverrideSelectionOptions())
	g.typingIndicatorOverrideSelection = widget.NewSelect(fyneUI.typingIndicatorOverrideSelectionOptions(), nil)
	g.refreshTypingIndicatorSettingSelection(fyneUI.readReceiptOverrideSelectionOptions())

	confirmClearHistory := dialog.NewConfirm(
		"Clear all message history?",
		"Are you sure you want to permanently delete all chat history on all devices?",
		func(confirmed bool) {
			if confirmed {
				err := fyneUI.callbacks.ClearGroupChatHistory(g.id)
				if err != nil {
					dialog.ShowError(errors.New("error clearing chat history: "+err.Error()), fyneUI.mainWindow)
				}
				fyneUI.showMainContainer()
				fyneUI.mainWindow.Canvas().Focus(g.getEntry())
			}
		},
		fyneUI.mainWindow,
	)
	g.clearHistoryButton = widget.NewButton("Clear History", func() {
		confirmClearHistory.Show()
	})

	confirmLeaveGroup := dialog.NewConfirm(
		"Leave Group?",
		"Are you sure you want to leave this group?",
		func(confirmed bool) {
			if confirmed {
				err := fyneUI.callbacks.RemoveUser(g.id, fyneUI.profile.id)
				if err != nil {
					dialog.ShowError(errors.New("error leaving group: "+err.Error()), fyneUI.mainWindow)
				}
				fyneUI.showMainContainer()
			}
		},
		fyneUI.mainWindow,
	)
	g.leaveGroupButton = widget.NewButton("Leave Group", func() {
		confirmLeaveGroup.Show()
	})

	confirmDeleteGroup := dialog.NewConfirm(
		"Delete this group?",
		"Are you sure you want to permanently delete this group for all members?",
		func(confirmed bool) {
			if confirmed {
				err := fyneUI.callbacks.DeleteGroup(g.id)
				if err != nil {
					dialog.ShowError(errors.New("error deleting group: "+err.Error()), fyneUI.mainWindow)
				}
				fyneUI.showMainContainer()
			}
		},
		fyneUI.mainWindow,
	)
	g.deleteGroupButton = widget.NewButton("Delete Group", func() {
		confirmDeleteGroup.Show()
	})

	confirmBlockGroup := dialog.NewConfirm(
		"Block this group?",
		"Are you sure you want to block this group?  You will never be able to join this group again.",
		func(confirmed bool) {
			if confirmed {
				err := fyneUI.callbacks.BlockGroup(g.id)
				if err != nil {
					dialog.ShowError(errors.New("error blocking group: "+err.Error()), fyneUI.mainWindow)
				}
				fyneUI.showMainContainer()
			}
		},
		fyneUI.mainWindow,
	)
	g.blockGroupButton = widget.NewButton("Block Group", func() {
		confirmBlockGroup.Show()
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
			err := fyneUI.callbacks.RenameGroup(g.id, strings.TrimSpace(newThreadName))
			if err != nil {
				dialog.ShowError(errors.New("error renaming group: "+err.Error()), fyneUI.mainWindow)
			}
		}

		// Update the notification settings if they have changes
		if g.notificationsEnabledCheck.Checked != (g.notificationsMutedUntil != chat.MutedForever) {
			mutedUntil := int64(0)
			if !g.notificationsEnabledCheck.Checked {
				mutedUntil = chat.MutedForever
			}
			err := fyneUI.callbacks.SetGroupMutedUntil(g.id, mutedUntil)
			if err != nil {
				dialog.ShowError(errors.New("error updating group mute settings: "+err.Error()), fyneUI.mainWindow)
			}
		}

		// Update the retention if it changed
		selectedRetentionString := g.retentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			dialog.ShowError(errors.New("invalid retention selection: "+selectedRetentionString), fyneUI.mainWindow)
		} else {
			if g.retention != selectedRetentionValue {
				err = fyneUI.callbacks.SetGroupRetention(g.id, selectedRetentionValue)
				if err != nil {
					dialog.ShowError(errors.New("error setting new retention value: "+err.Error()), fyneUI.mainWindow)
				}
			}
		}

		// Update the permissions if needed
		if g.restrictUserManagementCheck.Checked && !g.restrictUserManagement {
			err := fyneUI.callbacks.RestrictUserManagement(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error restricting user management: "+err.Error()), fyneUI.mainWindow)
			}
		}
		if !g.restrictUserManagementCheck.Checked && g.restrictUserManagement {
			err := fyneUI.callbacks.UnrestrictUserManagement(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error unrestricting user management: "+err.Error()), fyneUI.mainWindow)
			}
		}
		if g.restrictGroupEditsCheck.Checked && !g.restrictGroupEdits {
			err := fyneUI.callbacks.RestrictGroupEdits(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error restricting group edits: "+err.Error()), fyneUI.mainWindow)
			}
		}
		if !g.restrictGroupEditsCheck.Checked && g.restrictGroupEdits {
			err := fyneUI.callbacks.UnrestrictGroupEdits(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error unrestricting group edits: "+err.Error()), fyneUI.mainWindow)
			}
		}
		if g.restrictPostingCheck.Checked && !g.restrictPosting {
			err := fyneUI.callbacks.RestrictPosting(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error restricting posting: "+err.Error()), fyneUI.mainWindow)
			}
		}
		if !g.restrictPostingCheck.Checked && g.restrictPosting {
			err := fyneUI.callbacks.UnrestrictPosting(g.id)
			if err != nil {
				dialog.ShowError(errors.New("error unrestricting posting: "+err.Error()), fyneUI.mainWindow)
			}
		}

		// Update the read receipt override if needed
		defaultReadReceiptSelected := g.readReceiptOverrideSelection.Selected == defaultOn || g.readReceiptOverrideSelection.Selected == defaultOff
		switchedReadReceiptDefault := (defaultReadReceiptSelected && g.overrideReadReceiptSetting) || (!defaultReadReceiptSelected && !g.overrideReadReceiptSetting)
		switchedReadReceiptEnabled := (g.readReceiptOverrideSelection.Selected == off && g.readReceiptsEnabled) || (g.readReceiptOverrideSelection.Selected == on && !g.readReceiptsEnabled)
		if switchedReadReceiptDefault || switchedReadReceiptEnabled {
			if defaultReadReceiptSelected {
				fyneUI.callbacks.SetGroupReadReceiptSettings(g.id, false, true)
			} else if g.readReceiptOverrideSelection.Selected == on {
				fyneUI.callbacks.SetGroupReadReceiptSettings(g.id, true, true)
			} else if g.readReceiptOverrideSelection.Selected == off {
				fyneUI.callbacks.SetGroupReadReceiptSettings(g.id, true, false)
			} else {
				log.WithFields(log.Fields{
					"group_id":  g.id,
					"selection": g.readReceiptOverrideSelection.Selected,
				}).Error("invalid selection for read receipt override")
			}
		}

		// Update the typing indicator override if needed
		defaultTypingIndicatorSelected := g.typingIndicatorOverrideSelection.Selected == defaultOn || g.typingIndicatorOverrideSelection.Selected == defaultOff
		switchedTypingIndicatorDefault := (defaultTypingIndicatorSelected && g.overrideTypingIndicatorSetting) || (!defaultTypingIndicatorSelected && !g.overrideTypingIndicatorSetting)
		switchedTypingIndicatorEnabled := (g.typingIndicatorOverrideSelection.Selected == off && g.typingIndicatorsEnabled) || (g.typingIndicatorOverrideSelection.Selected == on && !g.typingIndicatorsEnabled)
		if switchedTypingIndicatorDefault || switchedTypingIndicatorEnabled {
			if defaultTypingIndicatorSelected {
				fyneUI.callbacks.SetGroupTypingIndicatorSettings(g.id, false, true)
			} else if g.typingIndicatorOverrideSelection.Selected == on {
				fyneUI.callbacks.SetGroupTypingIndicatorSettings(g.id, true, true)
			} else if g.typingIndicatorOverrideSelection.Selected == off {
				fyneUI.callbacks.SetGroupTypingIndicatorSettings(g.id, true, false)
			} else {
				log.WithFields(log.Fields{
					"group_id":  g.id,
					"selection": g.typingIndicatorOverrideSelection.Selected,
				}).Error("invalid selection for typing indicator override")
			}
		}

		if fyne.CurrentDevice().IsMobile() {
			fyneUI.mobileBack()
		} else {
			fyneUI.showMainContainer()
			fyneUI.mainWindow.Canvas().Focus(g.getEntry())
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
		fyneUI.refreshUserSelections(g)

		// Reset permissions
		g.restrictUserManagementCheck.SetChecked(g.restrictUserManagement)
		g.restrictGroupEditsCheck.SetChecked(g.restrictGroupEdits)
		g.restrictPostingCheck.SetChecked(g.restrictPosting)

		// Reset the read receipt and typing indicator overrides
		g.refreshReadReceiptSettingSelection(fyneUI.readReceiptOverrideSelectionOptions())
		g.refreshTypingIndicatorSettingSelection(fyneUI.readReceiptOverrideSelectionOptions())

		// Show main tontainer
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.mobileBack()
		} else {
			fyneUI.showMainContainer()
			fyneUI.mainWindow.Canvas().Focus(g.getEntry())
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
		fyneUI.refreshAvailableNewUsers(g, fyneUI.users.search(str))
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
	addUsersDialog := dialog.NewCustomConfirm("Add Users", "Save", "Cancel", newUserSelector, func(apply bool) {
		if apply {
			for _, thisUser := range g.pendingUsers.userList {
				err := fyneUI.callbacks.AddUser(g.id, thisUser.id)
				if err != nil {
					dialog.ShowError(errors.New("error adding user: "+err.Error()), fyneUI.mainWindow)
				}
			}
		} else {
			g.pendingUsers.empty()
			fyneUI.refreshUserSelections(g)
		}
		g.newUserSearchEntry.Text = ""
		g.newUserSearchEntry.Refresh()
		fyneUI.refreshAvailableNewUsers(g, fyneUI.users.search(g.newUserSearchEntry.Text))
		fyneUI.activeDialog = nil
		fyneUI.activeDialogCleanup = nil
	}, fyneUI.mainWindow)
	g.addUsersButton = widget.NewButton("Add Users", func() {
		fyneUI.activeDialog = addUsersDialog
		fyneUI.activeDialogCleanup = func() {
			g.pendingUsers.empty()
			fyneUI.refreshUserSelections(g)
			g.newUserSearchEntry.Text = ""
			g.newUserSearchEntry.Refresh()
			fyneUI.refreshAvailableNewUsers(g, fyneUI.users.search(g.newUserSearchEntry.Text))
		}
		addUsersDialog.Show()
	})

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), cancelChanges)
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	editGroupFeatures := container.NewVBox(
		container.NewCenter(groupIcon),
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

func (fyneUI *Fyne) refreshCurrentAndPendingUsers(g *group) {
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
				newDefaultImage(u.id, u.initials, theme.IconInlineSize()*2, nil),
				u.getName(),
				g.isAdmin(u.id),
				func() {
					fyneUI.getEditUserDialog(g, u.id).Show()
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
				newDefaultImage(u.id, u.initials, theme.IconInlineSize(), nil),
				u.getName(),
				false,
				func() {
					g.pendingUsers.remove(u.id)
					fyneUI.refreshUserSelections(g)
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

func (fyneUI *Fyne) refreshAvailableNewUsers(g *group, allAvailableUsers []*user) {
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
				newDefaultImage(u.id, u.initials, theme.IconInlineSize(), nil),
				u.getName(),
				false,
				func() {
					g.pendingUsers.add(u)
					fyneUI.refreshUserSelections(g)
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

func (fyneUI *Fyne) refreshUserSelections(g *group) {
	fyneUI.refreshCurrentAndPendingUsers(g)
	fyneUI.refreshAvailableNewUsers(g, fyneUI.users.search(g.newUserSearchEntry.Text))
}
