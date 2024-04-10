package ui

import (
	"errors"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
)

func (fyneUI *Fyne) showNewGroup() {
	fyneUI.clearNewGroupSelectors()
	fyneUI.mainWindow.SetContent(fyneUI.newGroup)
	fyneUI.newGroup.Show()
}

func (fyneUI *Fyne) clearNewGroupSelectors() {
	fyneUI.newGroupCreateButton.Enable()
	fyneUI.newGroupNameEntry.Enable()

	// TODO: reset the image

	fyneUI.newGroupNameEntry.Text = ""
	fyneUI.newGroupNameEntry.Refresh()

	fyneUI.newGroupPendingAdmins = map[uuid.UUID]bool{
		fyneUI.profile.id: true,
	}
	fyneUI.newGroupSelectedUsers.empty()
	fyneUI.newGroupPendingUsers.empty()
	fyneUI.newGroupPendingUsers.add(fyneUI.profile)
	fyneUI.refreshNewGroupUserSelections()

	fyneUI.newGroupRetentionSelection.Selected = getRetentionName(int64(time.Duration(24 * time.Hour * 7 * 4).Seconds())) // TODO: get default from database
	fyneUI.newGroupUserManagementRestrictedCheck.SetChecked(true)                                                         // TODO: get default from database
	fyneUI.newGroupGroupEditsRestrictedCheck.SetChecked(false)                                                            // TODO: get default from database
	fyneUI.newGroupPostingRestrictedCheck.SetChecked(false)                                                               // TODO: get default from database
}

func (fyneUI *Fyne) refreshNewGroupUserSelections() {
	// Add the currently selected users to the display
	currentUsersList := container.NewVBox()
	for _, thisUser := range fyneUI.newGroupSelectedUsers.alphabetized() {
		func(u *user) {
			removePendingUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() { // TODO: use the user's icon
				fyneUI.newGroupSelectedUsers.remove(u.id)
				fyneUI.refreshNewGroupUserSelections()
			})
			removePendingUserButton.Alignment = widget.ButtonAlignLeading
			removePendingUserButton.Importance = widget.LowImportance
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
	fyneUI.newGroupSelectedUsersContainer.SetMinSize(fyne.Size{Height: selectedUserHeight})
	fyneUI.newGroupSelectedUsersContainer.Refresh()

	// Update the available user to exclude these pending users
	allUsersListBox := container.NewVBox()
	for _, thisUser := range fyneUI.users.alphabetized() {
		// Exclude users that are pending addition to the group
		if _, exists := fyneUI.newGroupSelectedUsers.get(thisUser.id); exists {
			continue
		}
		if _, exists := fyneUI.newGroupPendingUsers.get(thisUser.id); exists {
			continue
		}

		func(u *user) {
			addUserButton := widget.NewButtonWithIcon(u.name, newEmbeddedResource("assets/not_found.png"), func() { // TODO: use the user's icon
				fyneUI.newGroupSelectedUsers.add(u)
				fyneUI.refreshNewGroupUserSelections()
			})
			addUserButton.Alignment = widget.ButtonAlignLeading
			addUserButton.Importance = widget.LowImportance
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
	fyneUI.newGroupAllAvailableUsers.SetMinSize(fyne.Size{Height: availableUserHeight})
	fyneUI.newGroupAllAvailableUsers.Refresh()

	// Refresh the users that have been selected for the new group
	pendingUsersList := container.NewVBox()
	for _, thisUser := range fyneUI.newGroupPendingUsers.alphabetized() {
		func(u *user) {
			userIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
			userIcon.FillMode = canvas.ImageFillContain
			userIcon.SetMinSize(fyne.NewSquareSize(theme.IconInlineSize()))
			userName := widget.NewLabel(u.name)
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
				adminCheck.SetChecked(true)
			}

			optionButtons := container.NewHBox(
				adminCheck,
				widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
					delete(fyneUI.newGroupPendingAdmins, u.id)
					fyneUI.newGroupPendingUsers.remove(u.id)
					fyneUI.refreshNewGroupUserSelections()
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
	fyneUI.newGroupPendingUsersList.Objects = []fyne.CanvasObject{pendingUsersList}
}

func (fyneUI *Fyne) buildNewGroup() {
	fyneUI.newGroupSelectedUsersContainer = container.NewVScroll(container.NewVBox())
	fyneUI.newGroupAllAvailableUsers = container.NewVScroll(container.NewVBox())
	fyneUI.newGroupPendingUsersList = container.NewMax()
	fyneUI.newGroupSelectedUsers = newUserStore()
	fyneUI.newGroupPendingUsers = newUserStore()
	fyneUI.newGroupPendingAdmins = map[uuid.UUID]bool{}

	newUserSearchEntry := widget.NewEntry() // TODO: on keypress, filter the available users
	newUserSelector := container.New(
		layout.NewBorderLayout(newUserSearchEntry, nil, nil, nil),
		newUserSearchEntry,
		container.NewVBox(
			widget.NewLabel("Users to add:"),
			fyneUI.newGroupSelectedUsersContainer,
			widget.NewLabel("All Users:"),
			fyneUI.newGroupAllAvailableUsers,
		),
	)
	addUsersDialog := dialog.NewCustomConfirm("Add Users", "Save", "Cancel", newUserSelector, func(apply bool) {
		if apply {
			for _, u := range fyneUI.newGroupSelectedUsers.userList {
				fyneUI.newGroupPendingUsers.add(u)
			}
		}
		fyneUI.newGroupSelectedUsers.empty()
		fyneUI.refreshNewGroupUserSelections()
	}, fyneUI.mainWindow)

	fyneUI.newGroupNameEntry = widget.NewEntry()

	fyneUI.newGroupAddUsersButton = widget.NewButton("Add Users", func() {
		addUsersDialog.Show()
	})
	fyneUI.newGroupRetentionSelection = widget.NewSelect(retentionSelections, nil)
	fyneUI.newGroupRetentionSelection.Selected = getRetentionName(int64(time.Duration(24 * time.Hour * 7 * 4).Seconds())) // TODO: get default from database
	fyneUI.newGroupUserManagementRestrictedCheck = widget.NewCheck("Restrict User Management", func(_ bool) {})
	fyneUI.newGroupUserManagementRestrictedCheck.SetChecked(true) // TODO: get default from database
	fyneUI.newGroupGroupEditsRestrictedCheck = widget.NewCheck("Restrict Group Edits", func(_ bool) {})
	fyneUI.newGroupGroupEditsRestrictedCheck.SetChecked(false) // TODO: get default from database
	fyneUI.newGroupPostingRestrictedCheck = widget.NewCheck("Restrict Posting", func(_ bool) {})
	fyneUI.newGroupPostingRestrictedCheck.SetChecked(false) // TODO: get default from database

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		fyneUI.showMainContainer()
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
			dialog.ShowError(errors.New("invalid retention selection: "+selectedRetentionString), fyneUI.mainWindow)
		}

		users := []chat.User{}
		for _, user := range fyneUI.newGroupSelectedUsers.alphabetized() {
			users = append(users, chat.User{ID: user.id})
		}

		admins := []uuid.UUID{}
		for k, _ := range fyneUI.newGroupPendingAdmins {
			admins = append(admins, k)
		}

		newGroup := chat.Group{
			Name: fyneUI.newGroupNameEntry.Text,
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
			fyneUI.newGroupCreateButton.Enable()
			fyneUI.newGroupNameEntry.Enable()
			dialog.ShowError(errors.New("Error creating group: "+err.Error()), fyneUI.mainWindow)
		}
	})
	fyneUI.newGroupCreateButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		fyneUI.showMainContainer()
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, fyneUI.newGroupCreateButton),
		fyneUI.newGroupCreateButton,
		cancelButton,
	)

	groupIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	groupIcon.FillMode = canvas.ImageFillContain
	groupIcon.SetMinSize(fyne.NewSize(64, 64))
	fyneUI.newGroup = container.New(
		layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
		closeBar,
		actionButtons,
		container.NewVBox(
			groupIcon,
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
