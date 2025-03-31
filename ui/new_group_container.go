package ui

import (
	"errors"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

func (fyneUI *Fyne) showNewGroup() {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeNewGroup})
	}
	fyneUI.clearNewGroupSelectors()
	fyneUI.mainWindow.SetContent(fyneUI.newGroup)
	fyneUI.newGroup.Show()
	if !fyne.CurrentDevice().IsMobile() {
		fyneUI.mainWindow.Canvas().Focus(fyneUI.newGroupNameEntry)
	}
}

func (fyneUI *Fyne) clearNewGroupSelectors() {
	fyneUI.newGroupCreateButton.Enable()
	fyneUI.newGroupNameEntry.Enable()

	// TODO: reset the image

	fyneUI.newGroupNameEntry.Text = ""
	fyneUI.newGroupNameEntry.Refresh()

	fyneUI.newGroupUserSearchEntry.Text = ""
	fyneUI.newGroupUserSearchEntry.Refresh()

	fyneUI.newGroupPendingAdmins = map[uuid.UUID]bool{
		fyneUI.profile.id: true,
	}
	fyneUI.newGroupSelectedUsers.empty()
	fyneUI.newGroupPendingUsers.empty()
	fyneUI.newGroupPendingUsers.add(fyneUI.profile)
	fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(fyneUI.newGroupUserSearchEntry.Text))

	fyneUI.newGroupRetentionSelection.Selected = getRetentionName(fyneUI.settings.DefaultGroupRetention)
	fyneUI.newGroupUserManagementRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictUserManagement)
	fyneUI.newGroupGroupEditsRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictGroupEdits)
	fyneUI.newGroupPostingRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictPosting)
}

func (fyneUI *Fyne) refreshNewGroupUserSelections(allAvailableUsers []*user) {
	// Add the currently selected users to the display
	currentUsersList := container.NewVBox()
	for _, thisUser := range fyneUI.newGroupSelectedUsers.alphabetized() {
		func(u *user) {
			// TODO: add listening to update button name with binding
			removePendingUserButton := newUserButton(
				newDefaultImage(u.id, u.initials, theme.IconInlineSize(), nil),
				u.getName(),
				false,
				func() {
					fyneUI.newGroupSelectedUsers.remove(u.id)
					fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(fyneUI.newGroupUserSearchEntry.Text))
				},
			)
			currentUsersList.Objects = append(
				currentUsersList.Objects,
				removePendingUserButton,
			)
		}(thisUser)
	}
	fyneUI.newGroupSelectedUsersContainer.Content = currentUsersList
	selectedUserHeight := float32(0)
	for i, obj := range currentUsersList.Objects {
		if i == 3 {
			break
		}
		selectedUserHeight += obj.MinSize().Height
	}
	selectedUserHeight += theme.Padding() * float32(len(currentUsersList.Objects)+1)
	fyneUI.newGroupSelectedUsersContainer.SetMinSize(fyne.Size{Height: selectedUserHeight})
	fyneUI.newGroupSelectedUsersContainer.Refresh()

	// Update the available user to exclude these pending users
	allUsersListBox := container.NewVBox()
	for _, thisUser := range allAvailableUsers {
		// Exclude users that are pending addition to the group
		if _, exists := fyneUI.newGroupSelectedUsers.get(thisUser.id); exists {
			continue
		}
		if _, exists := fyneUI.newGroupPendingUsers.get(thisUser.id); exists {
			continue
		}

		func(u *user) {
			// TODO: add listener to update button name with binding
			addUserButton := newUserButton(
				newDefaultImage(u.id, u.initials, theme.IconInlineSize(), nil),
				u.getName(),
				false,
				func() {
					fyneUI.newGroupSelectedUsers.add(u)
					fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(fyneUI.newGroupUserSearchEntry.Text))
				},
			)
			allUsersListBox.Objects = append(
				allUsersListBox.Objects,
				addUserButton,
			)
		}(thisUser)
	}
	fyneUI.newGroupAllAvailableUsers.Content = allUsersListBox
	availableUserHeight := float32(0)
	for i, obj := range allUsersListBox.Objects {
		if i == 3 {
			break
		}
		availableUserHeight += obj.MinSize().Height
	}
	availableUserHeight += theme.Padding() * float32(len(allUsersListBox.Objects)+1)
	fyneUI.newGroupAllAvailableUsers.SetMinSize(fyne.Size{Height: availableUserHeight})
	fyneUI.newGroupAllAvailableUsers.Refresh()

	// Refresh the users that have been selected for the new group
	pendingUsersList := container.NewVBox()
	for _, thisUser := range fyneUI.newGroupPendingUsers.alphabetized() {
		func(u *user) {
			userIcon := newDefaultImage(u.id, u.initials, theme.IconInlineSize()*2, nil)
			userName := widget.NewLabelWithData(u.name) // TODO: use RichText, and not and HBox, to support truncation
			userDetails := container.NewHBox(
				userIcon,
				userName,
			)

			adminCheck := widget.NewCheck("Admin", func(checked bool) {
				if checked {
					fyneUI.newGroupPendingAdmins[u.id] = true
				} else {
					delete(fyneUI.newGroupPendingAdmins, u.id)
				}
			})
			if _, present := fyneUI.newGroupPendingAdmins[u.id]; present {
				adminCheck.Checked = true
				adminCheck.Refresh()
			}

			optionButtons := container.NewHBox(
				adminCheck,
				widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
					delete(fyneUI.newGroupPendingAdmins, u.id)
					fyneUI.newGroupPendingUsers.remove(u.id)
					fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(fyneUI.newGroupUserSearchEntry.Text))
				}),
			)
			pendingUserRow := container.New(
				layout.NewBorderLayout(nil, nil, userDetails, optionButtons),
				userDetails,
				optionButtons,
			)

			pendingUsersList.Objects = append(
				pendingUsersList.Objects,
				pendingUserRow,
			)
		}(thisUser)
	}
	fyneUI.newGroupPendingUsersList.Content = pendingUsersList
	pendingUserHeight := float32(0)
	for i, obj := range pendingUsersList.Objects {
		if i == 6 {
			break
		}
		pendingUserHeight += obj.MinSize().Height
	}
	pendingUserHeight += theme.Padding() * float32(len(pendingUsersList.Objects)+1)
	fyneUI.newGroupPendingUsersList.SetMinSize(fyne.Size{Height: pendingUserHeight})
	fyneUI.newGroupPendingUsersList.Refresh()
}

func (fyneUI *Fyne) buildNewGroup() {
	fyneUI.newGroupSelectedUsersContainer = container.NewVScroll(container.NewVBox())
	fyneUI.newGroupAllAvailableUsers = container.NewVScroll(container.NewVBox())
	fyneUI.newGroupPendingUsersList = container.NewVScroll(container.NewVBox())
	fyneUI.newGroupSelectedUsers = newUserStore()
	fyneUI.newGroupPendingUsers = newUserStore()
	fyneUI.newGroupPendingAdmins = map[uuid.UUID]bool{}

	fyneUI.newGroupUserSearchEntry = widget.NewEntry()
	fyneUI.newGroupUserSearchEntry.OnChanged = func(str string) {
		fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(str))
	}
	newUserSelector := container.New(
		layout.NewBorderLayout(fyneUI.newGroupUserSearchEntry, nil, nil, nil),
		fyneUI.newGroupUserSearchEntry,
		container.NewVBox(
			widget.NewLabel("Users to add:"),
			fyneUI.newGroupSelectedUsersContainer,
			widget.NewLabel("All Users:"),
			fyneUI.newGroupAllAvailableUsers,
		),
	)
	addUsersDialogCleanup := func() {
		fyneUI.newGroupSelectedUsers.empty()
		fyneUI.newGroupUserSearchEntry.Text = ""
		fyneUI.newGroupUserSearchEntry.Refresh()
		fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(""))
	}
	addUsersDialog := dialog.NewCustomConfirm("Add Users", "Save", "Cancel", newUserSelector, func(apply bool) {
		if apply {
			for _, u := range fyneUI.newGroupSelectedUsers.userList {
				fyneUI.newGroupPendingUsers.add(u)
			}
		}
	}, fyneUI.mainWindow)

	fyneUI.newGroupNameEntry = widget.NewEntry()

	fyneUI.newGroupAddUsersButton = widget.NewButton("Add Users", func() {
		fyneUI.showDialog(addUsersDialog, addUsersDialogCleanup)
	})
	fyneUI.newGroupRetentionSelection = widget.NewSelect(retentionSelections, nil)
	fyneUI.newGroupRetentionSelection.Selected = getRetentionName(fyneUI.settings.DefaultGroupRetention)
	fyneUI.newGroupUserManagementRestrictedCheck = widget.NewCheck("Restrict User Management", func(_ bool) {})
	fyneUI.newGroupUserManagementRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictUserManagement)
	fyneUI.newGroupGroupEditsRestrictedCheck = widget.NewCheck("Restrict Group Edits", func(_ bool) {})
	fyneUI.newGroupGroupEditsRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictGroupEdits)
	fyneUI.newGroupPostingRestrictedCheck = widget.NewCheck("Restrict Posting", func(_ bool) {})
	fyneUI.newGroupPostingRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictPosting)

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

	fyneUI.newGroupCreateButton = widget.NewButton("Create", func() {
		fyneUI.newGroupCreateButton.Disable()
		fyneUI.newGroupNameEntry.Disable()

		selectedRetentionString := fyneUI.newGroupRetentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			fyneUI.showDialog(dialog.NewError(errors.New("invalid retention selection: "+selectedRetentionString), fyneUI.mainWindow), nil)
		}

		users := []chat.User{}
		for _, user := range fyneUI.newGroupPendingUsers.alphabetized() {
			users = append(users, chat.User{ID: user.id})
		}

		admins := []uuid.UUID{}
		for k, _ := range fyneUI.newGroupPendingAdmins {
			admins = append(admins, k)
		}

		newGroup := chat.Group{
			Name: strings.TrimSpace(fyneUI.newGroupNameEntry.Text),
			//Image:
			Users:                  users,
			Admins:                 admins,
			Retention:              selectedRetentionValue,
			RestrictUserManagement: fyneUI.newGroupUserManagementRestrictedCheck.Checked,
			RestrictGroupEdits:     fyneUI.newGroupGroupEditsRestrictedCheck.Checked,
			RestrictPosting:        fyneUI.newGroupPostingRestrictedCheck.Checked,
		}

		err := fyneUI.callbacks.CreateGroup(newGroup)
		if err != nil {
			fyneUI.showDialog(dialog.NewError(errors.New("Error creating group: "+err.Error()), fyneUI.mainWindow), nil)
		}
		fyneUI.newGroupCreateButton.Enable()
		fyneUI.newGroupNameEntry.Enable()
	})
	fyneUI.newGroupCreateButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.mobileBack()
		} else {
			fyneUI.showMainContainer()
		}
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, fyneUI.newGroupCreateButton),
		fyneUI.newGroupCreateButton,
		cancelButton,
	)

	groupIconName := binding.NewString()
	fyneUI.newGroupNameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		fyneUI.newGroupNameEntry.Text = str
		if trimmed != 0 {
			fyneUI.newGroupNameEntry.CursorRow = 0
			fyneUI.newGroupNameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			fyneUI.newGroupNameEntry.Text = string(truncated)

		}
		fyneUI.newGroupNameEntry.Refresh()

		// Set the group icon
		r, _ := utf8.DecodeRuneInString(str)
		if r == utf8.RuneError {
			groupIconName.Set("")
		} else {
			groupIconName.Set(string(r))
		}
	}
	groupIcon := newDefaultImage(uuid.Nil, groupIconName, 128, func() {
		log.Info("user wants to select the image for a new group")
	})

	fyneUI.newGroup = container.New(
		layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
		closeBar,
		actionButtons,
		container.NewVBox(
			container.NewCenter(groupIcon),
			fyneUI.newGroupNameEntry,
			widget.NewLabel("Users:"),
			fyneUI.newGroupPendingUsersList,
			container.NewHBox(fyneUI.newGroupAddUsersButton),
			widget.NewAccordion(
				&widget.AccordionItem{
					Title: "Advanced Options",
					Detail: container.NewVBox(
						widget.NewLabel("Disappearing Messages"),
						fyneUI.newGroupRetentionSelection,
						widget.NewLabel("Permissions"),
						fyneUI.newGroupUserManagementRestrictedCheck,
						fyneUI.newGroupGroupEditsRestrictedCheck,
						fyneUI.newGroupPostingRestrictedCheck,
					),
				},
			),
		),
	)
}
