package ui

import (
	"errors"
	"io"
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

type newGroupWidgets struct {
	icon                          *defaultImage
	iconData                      []byte
	iconName                      binding.String
	nameEntry                     *widget.Entry
	selectedUsersContainer        *container.Scroll
	pendingUsersList              *container.Scroll
	allAvailableUsers             *container.Scroll
	userSearchEntry               *widget.Entry
	selectedUsers                 *userStore
	pendingUsers                  *userStore
	pendingAdmins                 map[uuid.UUID]bool
	createButton                  *widget.Button
	addAdminsButton               *widget.Button
	addUsersButton                *widget.Button
	retentionSelection            *widget.Select
	userManagementRestrictedCheck *widget.Check
	groupEditsRestrictedCheck     *widget.Check
	postingRestrictedCheck        *widget.Check
}

func (fyneUI *Fyne) buildNewGroup() {
	fyneUI.newGroupWidgets = &newGroupWidgets{
		nameEntry:              widget.NewEntry(),
		iconData:               []byte{},
		selectedUsersContainer: container.NewVScroll(container.NewVBox()),
		allAvailableUsers:      container.NewVScroll(container.NewVBox()),
		pendingUsersList:       container.NewVScroll(container.NewVBox()),
		selectedUsers:          newUserStore(),
		pendingUsers:           newUserStore(),
		pendingAdmins:          map[uuid.UUID]bool{},
	}

	fyneUI.newGroupWidgets.userSearchEntry = widget.NewEntry()
	fyneUI.newGroupWidgets.userSearchEntry.OnChanged = func(str string) {
		fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(str))
	}
	newUserSelector := container.New(
		layout.NewBorderLayout(fyneUI.newGroupWidgets.userSearchEntry, nil, nil, nil),
		fyneUI.newGroupWidgets.userSearchEntry,
		container.NewVBox(
			widget.NewLabel("Users to add:"),
			fyneUI.newGroupWidgets.selectedUsersContainer,
			widget.NewLabel("All Users:"),
			fyneUI.newGroupWidgets.allAvailableUsers,
		),
	)
	addUsersDialogCleanup := func() {
		fyneUI.newGroupWidgets.selectedUsers.empty()
		fyneUI.newGroupWidgets.userSearchEntry.Text = ""
		fyneUI.newGroupWidgets.userSearchEntry.Refresh()
		fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(""))
	}
	addUsersDialog := dialog.NewCustomConfirm("Add Users", "Save", "Cancel", newUserSelector, func(apply bool) {
		if apply {
			for _, u := range fyneUI.newGroupWidgets.selectedUsers.userList {
				fyneUI.newGroupWidgets.pendingUsers.add(u)
			}
		}
	}, fyneUI.mainWindow)

	fyneUI.newGroupWidgets.addUsersButton = widget.NewButton("Add Users", func() {
		fyneUI.showDialog(addUsersDialog, addUsersDialogCleanup)
	})
	fyneUI.newGroupWidgets.retentionSelection = widget.NewSelect(retentionSelections, nil)
	fyneUI.newGroupWidgets.retentionSelection.Selected = getRetentionName(fyneUI.settings.DefaultGroupRetention)
	fyneUI.newGroupWidgets.userManagementRestrictedCheck = widget.NewCheck("Restrict User Management", func(_ bool) {})
	fyneUI.newGroupWidgets.userManagementRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictUserManagement)
	fyneUI.newGroupWidgets.groupEditsRestrictedCheck = widget.NewCheck("Restrict Group Edits", func(_ bool) {})
	fyneUI.newGroupWidgets.groupEditsRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictGroupEdits)
	fyneUI.newGroupWidgets.postingRestrictedCheck = widget.NewCheck("Restrict Posting", func(_ bool) {})
	fyneUI.newGroupWidgets.postingRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictPosting)

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

	fyneUI.newGroupWidgets.createButton = widget.NewButton("Create", func() {
		fyneUI.newGroupWidgets.createButton.Disable()
		fyneUI.newGroupWidgets.nameEntry.Disable()

		selectedRetentionString := fyneUI.newGroupWidgets.retentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			fyneUI.showDialog(dialog.NewError(errors.New("invalid retention selection: "+selectedRetentionString), fyneUI.mainWindow), nil)
		}

		users := []chat.User{}
		for _, user := range fyneUI.newGroupWidgets.pendingUsers.alphabetized() {
			users = append(users, chat.User{ID: user.id})
		}

		admins := []uuid.UUID{}
		for k, _ := range fyneUI.newGroupWidgets.pendingAdmins {
			admins = append(admins, k)
		}

		newGroup := chat.Group{
			Name: strings.TrimSpace(fyneUI.newGroupWidgets.nameEntry.Text),
			//Image:
			Users:                  users,
			Admins:                 admins,
			Retention:              selectedRetentionValue,
			RestrictUserManagement: fyneUI.newGroupWidgets.userManagementRestrictedCheck.Checked,
			RestrictGroupEdits:     fyneUI.newGroupWidgets.groupEditsRestrictedCheck.Checked,
			RestrictPosting:        fyneUI.newGroupWidgets.postingRestrictedCheck.Checked,
		}

		err := fyneUI.callbacks.CreateGroup(newGroup, fyneUI.newGroupWidgets.iconData)
		if err != nil {
			fyneUI.showDialog(dialog.NewError(errors.New("Error creating group: "+err.Error()), fyneUI.mainWindow), nil)
		}
		fyneUI.newGroupWidgets.createButton.Enable()
		fyneUI.newGroupWidgets.nameEntry.Enable()
	})
	fyneUI.newGroupWidgets.createButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.mobileBack()
		} else {
			fyneUI.showMainContainer()
		}
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, fyneUI.newGroupWidgets.createButton),
		fyneUI.newGroupWidgets.createButton,
		cancelButton,
	)

	fyneUI.newGroupWidgets.iconName = binding.NewString()
	fyneUI.newGroupWidgets.nameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		fyneUI.newGroupWidgets.nameEntry.Text = str
		if trimmed != 0 {
			fyneUI.newGroupWidgets.nameEntry.CursorRow = 0
			fyneUI.newGroupWidgets.nameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			fyneUI.newGroupWidgets.nameEntry.Text = string(truncated)

		}
		fyneUI.newGroupWidgets.nameEntry.Refresh()

		// Set the group icon
		r, _ := utf8.DecodeRuneInString(str)
		if r == utf8.RuneError {
			fyneUI.newGroupWidgets.iconName.Set("")
		} else {
			fyneUI.newGroupWidgets.iconName.Set(string(r))
		}
	}
	fileGetter := func(_ uuid.UUID) ([]byte, error) {
		return fyneUI.newGroupWidgets.iconData, nil
	}
	fyneUI.newGroupWidgets.icon = newDefaultImage(uuid.Nil, []uuid.UUID{}, fyneUI.newGroupWidgets.iconName, 128, fileGetter, func() {
		dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if reader == nil {
				return
			}

			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Debug("error selecting image for new group")
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

			fyneUI.newGroupWidgets.iconData = data
			fyneUI.newGroupWidgets.icon.images = []uuid.UUID{uuid.New()}
			fyneUI.newGroupWidgets.icon.Refresh()
		}, fyneUI.mainWindow).Show() // We do not use showDialog here because on mobile this uses a native intent
	})

	fyneUI.newGroup = container.New(
		layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
		closeBar,
		actionButtons,
		container.NewVBox(
			container.NewCenter(fyneUI.newGroupWidgets.icon),
			fyneUI.newGroupWidgets.nameEntry,
			widget.NewLabel("Users:"),
			fyneUI.newGroupWidgets.pendingUsersList,
			container.NewHBox(fyneUI.newGroupWidgets.addUsersButton),
			widget.NewAccordion(
				&widget.AccordionItem{
					Title: "Advanced Options",
					Detail: container.NewVBox(
						widget.NewLabel("Disappearing Messages"),
						fyneUI.newGroupWidgets.retentionSelection,
						widget.NewLabel("Permissions"),
						fyneUI.newGroupWidgets.userManagementRestrictedCheck,
						fyneUI.newGroupWidgets.groupEditsRestrictedCheck,
						fyneUI.newGroupWidgets.postingRestrictedCheck,
					),
				},
			),
		),
	)
}

func (fyneUI *Fyne) showNewGroup() {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeNewGroup})
	}
	fyneUI.clearNewGroupSelectors()
	fyneUI.mainWindow.SetContent(fyneUI.newGroup)
	fyneUI.newGroup.Show()
	if !fyne.CurrentDevice().IsMobile() {
		fyneUI.mainWindow.Canvas().Focus(fyneUI.newGroupWidgets.nameEntry)
	}
}

func (fyneUI *Fyne) clearNewGroupSelectors() {
	fyneUI.newGroupWidgets.createButton.Enable()
	fyneUI.newGroupWidgets.nameEntry.Enable()

	fyneUI.newGroupWidgets.iconName.Set("")
	fyneUI.newGroupWidgets.iconData = []byte{}
	fyneUI.newGroupWidgets.icon.images = []uuid.UUID{}
	fyneUI.newGroupWidgets.icon.Refresh()

	fyneUI.newGroupWidgets.nameEntry.Text = ""
	fyneUI.newGroupWidgets.nameEntry.Refresh()

	fyneUI.newGroupWidgets.userSearchEntry.Text = ""
	fyneUI.newGroupWidgets.userSearchEntry.Refresh()

	fyneUI.newGroupWidgets.pendingAdmins = map[uuid.UUID]bool{
		fyneUI.profile.id: true,
	}
	fyneUI.newGroupWidgets.selectedUsers.empty()
	fyneUI.newGroupWidgets.pendingUsers.empty()
	fyneUI.newGroupWidgets.pendingUsers.add(fyneUI.profile)
	fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(fyneUI.newGroupWidgets.userSearchEntry.Text))

	fyneUI.newGroupWidgets.retentionSelection.Selected = getRetentionName(fyneUI.settings.DefaultGroupRetention)
	fyneUI.newGroupWidgets.userManagementRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictUserManagement)
	fyneUI.newGroupWidgets.groupEditsRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictGroupEdits)
	fyneUI.newGroupWidgets.postingRestrictedCheck.SetChecked(fyneUI.settings.NewGroupRestrictPosting)
}

func (fyneUI *Fyne) refreshNewGroupUserSelections(allAvailableUsers []*user) {
	// Add the currently selected users to the display
	currentUsersList := container.NewVBox()
	for _, thisUser := range fyneUI.newGroupWidgets.selectedUsers.alphabetized() {
		func(u *user) {
			// TODO: add listening to update button name with binding
			removePendingUserButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize(), fyneUI.callbacks.GetFileData, nil),
				u.getName(),
				false,
				func() {
					fyneUI.newGroupWidgets.selectedUsers.remove(u.id)
					fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(fyneUI.newGroupWidgets.userSearchEntry.Text))
				},
			)
			currentUsersList.Objects = append(
				currentUsersList.Objects,
				removePendingUserButton,
			)
		}(thisUser)
	}
	fyneUI.newGroupWidgets.selectedUsersContainer.Content = currentUsersList
	selectedUserHeight := float32(0)
	for i, obj := range currentUsersList.Objects {
		if i == 3 {
			break
		}
		selectedUserHeight += obj.MinSize().Height
	}
	selectedUserHeight += theme.Padding() * float32(len(currentUsersList.Objects)+1)
	fyneUI.newGroupWidgets.selectedUsersContainer.SetMinSize(fyne.Size{Height: selectedUserHeight})
	fyneUI.newGroupWidgets.selectedUsersContainer.Refresh()

	// Update the available user to exclude these pending users
	allUsersListBox := container.NewVBox()
	for _, thisUser := range allAvailableUsers {
		// Exclude users that are pending addition to the group
		if _, exists := fyneUI.newGroupWidgets.selectedUsers.get(thisUser.id); exists {
			continue
		}
		if _, exists := fyneUI.newGroupWidgets.pendingUsers.get(thisUser.id); exists {
			continue
		}

		func(u *user) {
			// TODO: add listener to update button name with binding
			addUserButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize(), fyneUI.callbacks.GetFileData, nil),
				u.getName(),
				false,
				func() {
					fyneUI.newGroupWidgets.selectedUsers.add(u)
					fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(fyneUI.newGroupWidgets.userSearchEntry.Text))
				},
			)
			allUsersListBox.Objects = append(
				allUsersListBox.Objects,
				addUserButton,
			)
		}(thisUser)
	}
	fyneUI.newGroupWidgets.allAvailableUsers.Content = allUsersListBox
	availableUserHeight := float32(0)
	for i, obj := range allUsersListBox.Objects {
		if i == 3 {
			break
		}
		availableUserHeight += obj.MinSize().Height
	}
	availableUserHeight += theme.Padding() * float32(len(allUsersListBox.Objects)+1)
	fyneUI.newGroupWidgets.allAvailableUsers.SetMinSize(fyne.Size{Height: availableUserHeight})
	fyneUI.newGroupWidgets.allAvailableUsers.Refresh()

	// Refresh the users that have been selected for the new group
	pendingUsersList := container.NewVBox()
	for _, thisUser := range fyneUI.newGroupWidgets.pendingUsers.alphabetized() {
		func(u *user) {
			userIcon := newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, fyneUI.callbacks.GetFileData, nil)
			userName := widget.NewLabelWithData(u.name) // TODO: use RichText, and not and HBox, to support truncation
			userDetails := container.NewHBox(
				userIcon,
				userName,
			)

			adminCheck := widget.NewCheck("Admin", func(checked bool) {
				if checked {
					fyneUI.newGroupWidgets.pendingAdmins[u.id] = true
				} else {
					delete(fyneUI.newGroupWidgets.pendingAdmins, u.id)
				}
			})
			if _, present := fyneUI.newGroupWidgets.pendingAdmins[u.id]; present {
				adminCheck.Checked = true
				adminCheck.Refresh()
			}

			optionButtons := container.NewHBox(
				adminCheck,
				widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
					delete(fyneUI.newGroupWidgets.pendingAdmins, u.id)
					fyneUI.newGroupWidgets.pendingUsers.remove(u.id)
					fyneUI.refreshNewGroupUserSelections(fyneUI.users.search(fyneUI.newGroupWidgets.userSearchEntry.Text))
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
	fyneUI.newGroupWidgets.pendingUsersList.Content = pendingUsersList
	pendingUserHeight := float32(0)
	for i, obj := range pendingUsersList.Objects {
		if i == 6 {
			break
		}
		pendingUserHeight += obj.MinSize().Height
	}
	pendingUserHeight += theme.Padding() * float32(len(pendingUsersList.Objects)+1)
	fyneUI.newGroupWidgets.pendingUsersList.SetMinSize(fyne.Size{Height: pendingUserHeight})
	fyneUI.newGroupWidgets.pendingUsersList.Refresh()
}
