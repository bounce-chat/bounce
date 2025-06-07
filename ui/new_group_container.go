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

func (ui *ui) buildNewGroup() {
	ui.newGroupWidgets = &newGroupWidgets{
		nameEntry:              widget.NewEntry(),
		iconData:               []byte{},
		selectedUsersContainer: container.NewVScroll(container.NewVBox()),
		allAvailableUsers:      container.NewVScroll(container.NewVBox()),
		pendingUsersList:       container.NewVScroll(container.NewVBox()),
		selectedUsers:          newUserStore(),
		pendingUsers:           newUserStore(),
		pendingAdmins:          map[uuid.UUID]bool{},
	}

	ui.newGroupWidgets.userSearchEntry = widget.NewEntry()
	ui.newGroupWidgets.userSearchEntry.OnChanged = func(str string) {
		ui.refreshNewGroupUserSelections(ui.users.search(str))
	}
	newUserSelector := container.New(
		layout.NewBorderLayout(ui.newGroupWidgets.userSearchEntry, nil, nil, nil),
		ui.newGroupWidgets.userSearchEntry,
		container.NewVBox(
			widget.NewLabel("Users to add:"),
			ui.newGroupWidgets.selectedUsersContainer,
			widget.NewLabel("All Users:"),
			ui.newGroupWidgets.allAvailableUsers,
		),
	)
	addUsersDialogCleanup := func() {
		ui.newGroupWidgets.selectedUsers.empty()
		ui.newGroupWidgets.userSearchEntry.Text = ""
		ui.newGroupWidgets.userSearchEntry.Refresh()
		ui.refreshNewGroupUserSelections(ui.users.search(""))
	}
	addUsersDialog := dialog.NewCustomConfirm("Add Users", "Save", "Cancel", newUserSelector, func(apply bool) {
		if apply {
			for _, u := range ui.newGroupWidgets.selectedUsers.userList {
				ui.newGroupWidgets.pendingUsers.add(u)
			}
		}
	}, ui.mainWindow)

	ui.newGroupWidgets.addUsersButton = widget.NewButton("Add Users", func() {
		ui.showDialog(addUsersDialog, addUsersDialogCleanup)
	})
	ui.newGroupWidgets.retentionSelection = widget.NewSelect(retentionSelections, nil)
	ui.newGroupWidgets.retentionSelection.Selected = getRetentionName(ui.settings.DefaultGroupRetention)
	ui.newGroupWidgets.userManagementRestrictedCheck = widget.NewCheck("Restrict User Management", func(_ bool) {})
	ui.newGroupWidgets.userManagementRestrictedCheck.SetChecked(ui.settings.NewGroupRestrictUserManagement)
	ui.newGroupWidgets.groupEditsRestrictedCheck = widget.NewCheck("Restrict Group Edits", func(_ bool) {})
	ui.newGroupWidgets.groupEditsRestrictedCheck.SetChecked(ui.settings.NewGroupRestrictGroupEdits)
	ui.newGroupWidgets.postingRestrictedCheck = widget.NewCheck("Restrict Posting", func(_ bool) {})
	ui.newGroupWidgets.postingRestrictedCheck.SetChecked(ui.settings.NewGroupRestrictPosting)

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

	ui.newGroupWidgets.createButton = widget.NewButton("Create", func() {
		ui.newGroupWidgets.createButton.Disable()
		ui.newGroupWidgets.nameEntry.Disable()

		selectedRetentionString := ui.newGroupWidgets.retentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			ui.showDialog(dialog.NewError(errors.New("invalid retention selection: "+selectedRetentionString), ui.mainWindow), nil)
		}

		users := []chat.User{}
		for _, user := range ui.newGroupWidgets.pendingUsers.alphabetized() {
			users = append(users, chat.User{ID: user.id})
		}

		admins := []uuid.UUID{}
		for k, _ := range ui.newGroupWidgets.pendingAdmins {
			admins = append(admins, k)
		}

		newGroup := chat.Group{
			Name: strings.TrimSpace(ui.newGroupWidgets.nameEntry.Text),
			//Image:
			Users:                  users,
			Admins:                 admins,
			Retention:              selectedRetentionValue,
			RestrictUserManagement: ui.newGroupWidgets.userManagementRestrictedCheck.Checked,
			RestrictGroupEdits:     ui.newGroupWidgets.groupEditsRestrictedCheck.Checked,
			RestrictPosting:        ui.newGroupWidgets.postingRestrictedCheck.Checked,
		}

		err := ui.bounce.CreateGroup(newGroup, ui.newGroupWidgets.iconData)
		if err != nil {
			ui.showDialog(dialog.NewError(errors.New("Error creating group: "+err.Error()), ui.mainWindow), nil)
		}
		ui.newGroupWidgets.createButton.Enable()
		ui.newGroupWidgets.nameEntry.Enable()
	})
	ui.newGroupWidgets.createButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
		}
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, ui.newGroupWidgets.createButton),
		ui.newGroupWidgets.createButton,
		cancelButton,
	)

	ui.newGroupWidgets.iconName = binding.NewString()
	ui.newGroupWidgets.nameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		ui.newGroupWidgets.nameEntry.Text = str
		if trimmed != 0 {
			ui.newGroupWidgets.nameEntry.CursorRow = 0
			ui.newGroupWidgets.nameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			ui.newGroupWidgets.nameEntry.Text = string(truncated)

		}
		ui.newGroupWidgets.nameEntry.Refresh()

		// Set the group icon
		r, _ := utf8.DecodeRuneInString(str)
		if r == utf8.RuneError {
			ui.newGroupWidgets.iconName.Set("")
		} else {
			ui.newGroupWidgets.iconName.Set(string(r))
		}
	}
	fileGetter := func(_ uuid.UUID) ([]byte, error) {
		return ui.newGroupWidgets.iconData, nil
	}
	ui.newGroupWidgets.icon = newDefaultImage(uuid.Nil, []uuid.UUID{}, ui.newGroupWidgets.iconName, 128, fileGetter, func() {
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

			ui.newGroupWidgets.iconData = data
			ui.newGroupWidgets.icon.images = []uuid.UUID{uuid.New()}
			ui.newGroupWidgets.icon.Refresh()
		}, ui.mainWindow).Show() // We do not use showDialog here because on mobile this uses a native intent
	})

	ui.newGroup = container.New(
		layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
		closeBar,
		actionButtons,
		container.NewVBox(
			container.NewCenter(ui.newGroupWidgets.icon),
			ui.newGroupWidgets.nameEntry,
			widget.NewLabel("Users:"),
			ui.newGroupWidgets.pendingUsersList,
			container.NewHBox(ui.newGroupWidgets.addUsersButton),
			widget.NewAccordion(
				&widget.AccordionItem{
					Title: "Advanced Options",
					Detail: container.NewVBox(
						widget.NewLabel("Disappearing Messages"),
						ui.newGroupWidgets.retentionSelection,
						widget.NewLabel("Permissions"),
						ui.newGroupWidgets.userManagementRestrictedCheck,
						ui.newGroupWidgets.groupEditsRestrictedCheck,
						ui.newGroupWidgets.postingRestrictedCheck,
					),
				},
			),
		),
	)
}

func (ui *ui) showNewGroup() {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeNewGroup})
	}
	ui.clearNewGroupSelectors()
	ui.mainWindow.SetContent(ui.newGroup)
	ui.newGroup.Show()
	if !fyne.CurrentDevice().IsMobile() {
		ui.mainWindow.Canvas().Focus(ui.newGroupWidgets.nameEntry)
	}
}

func (ui *ui) clearNewGroupSelectors() {
	ui.newGroupWidgets.createButton.Enable()
	ui.newGroupWidgets.nameEntry.Enable()

	ui.newGroupWidgets.iconName.Set("")
	ui.newGroupWidgets.iconData = []byte{}
	ui.newGroupWidgets.icon.images = []uuid.UUID{}
	ui.newGroupWidgets.icon.Refresh()

	ui.newGroupWidgets.nameEntry.Text = ""
	ui.newGroupWidgets.nameEntry.Refresh()

	ui.newGroupWidgets.userSearchEntry.Text = ""
	ui.newGroupWidgets.userSearchEntry.Refresh()

	ui.newGroupWidgets.pendingAdmins = map[uuid.UUID]bool{
		ui.profile.id: true,
	}
	ui.newGroupWidgets.selectedUsers.empty()
	ui.newGroupWidgets.pendingUsers.empty()
	ui.newGroupWidgets.pendingUsers.add(ui.profile)
	ui.refreshNewGroupUserSelections(ui.users.search(ui.newGroupWidgets.userSearchEntry.Text))

	ui.newGroupWidgets.retentionSelection.Selected = getRetentionName(ui.settings.DefaultGroupRetention)
	ui.newGroupWidgets.userManagementRestrictedCheck.SetChecked(ui.settings.NewGroupRestrictUserManagement)
	ui.newGroupWidgets.groupEditsRestrictedCheck.SetChecked(ui.settings.NewGroupRestrictGroupEdits)
	ui.newGroupWidgets.postingRestrictedCheck.SetChecked(ui.settings.NewGroupRestrictPosting)
}

func (ui *ui) refreshNewGroupUserSelections(allAvailableUsers []*user) {
	// Add the currently selected users to the display
	currentUsersList := container.NewVBox()
	for _, thisUser := range ui.newGroupWidgets.selectedUsers.alphabetized() {
		func(u *user) {
			// TODO: add listening to update button name with binding
			removePendingUserButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize(), ui.bounce.GetFileData, nil),
				u.getName(),
				false,
				func() {
					ui.newGroupWidgets.selectedUsers.remove(u.id)
					ui.refreshNewGroupUserSelections(ui.users.search(ui.newGroupWidgets.userSearchEntry.Text))
				},
			)
			currentUsersList.Objects = append(
				currentUsersList.Objects,
				removePendingUserButton,
			)
		}(thisUser)
	}
	ui.newGroupWidgets.selectedUsersContainer.Content = currentUsersList
	selectedUserHeight := float32(0)
	for i, obj := range currentUsersList.Objects {
		if i == 3 {
			break
		}
		selectedUserHeight += obj.MinSize().Height
	}
	selectedUserHeight += theme.Padding() * float32(len(currentUsersList.Objects)+1)
	ui.newGroupWidgets.selectedUsersContainer.SetMinSize(fyne.Size{Height: selectedUserHeight})
	ui.newGroupWidgets.selectedUsersContainer.Refresh()

	// Update the available user to exclude these pending users
	allUsersListBox := container.NewVBox()
	for _, thisUser := range allAvailableUsers {
		// Exclude users that are pending addition to the group
		if _, exists := ui.newGroupWidgets.selectedUsers.get(thisUser.id); exists {
			continue
		}
		if _, exists := ui.newGroupWidgets.pendingUsers.get(thisUser.id); exists {
			continue
		}

		func(u *user) {
			// TODO: add listener to update button name with binding
			addUserButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize(), ui.bounce.GetFileData, nil),
				u.getName(),
				false,
				func() {
					ui.newGroupWidgets.selectedUsers.add(u)
					ui.refreshNewGroupUserSelections(ui.users.search(ui.newGroupWidgets.userSearchEntry.Text))
				},
			)
			allUsersListBox.Objects = append(
				allUsersListBox.Objects,
				addUserButton,
			)
		}(thisUser)
	}
	ui.newGroupWidgets.allAvailableUsers.Content = allUsersListBox
	availableUserHeight := float32(0)
	for i, obj := range allUsersListBox.Objects {
		if i == 3 {
			break
		}
		availableUserHeight += obj.MinSize().Height
	}
	availableUserHeight += theme.Padding() * float32(len(allUsersListBox.Objects)+1)
	ui.newGroupWidgets.allAvailableUsers.SetMinSize(fyne.Size{Height: availableUserHeight})
	ui.newGroupWidgets.allAvailableUsers.Refresh()

	// Refresh the users that have been selected for the new group
	pendingUsersList := container.NewVBox()
	for _, thisUser := range ui.newGroupWidgets.pendingUsers.alphabetized() {
		func(u *user) {
			userIcon := newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, ui.bounce.GetFileData, nil)
			userName := widget.NewLabelWithData(u.name) // TODO: use RichText, and not and HBox, to support truncation
			userDetails := container.NewHBox(
				userIcon,
				userName,
			)

			adminCheck := widget.NewCheck("Admin", func(checked bool) {
				if checked {
					ui.newGroupWidgets.pendingAdmins[u.id] = true
				} else {
					delete(ui.newGroupWidgets.pendingAdmins, u.id)
				}
			})
			if _, present := ui.newGroupWidgets.pendingAdmins[u.id]; present {
				adminCheck.Checked = true
				adminCheck.Refresh()
			}

			optionButtons := container.NewHBox(
				adminCheck,
				widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
					delete(ui.newGroupWidgets.pendingAdmins, u.id)
					ui.newGroupWidgets.pendingUsers.remove(u.id)
					ui.refreshNewGroupUserSelections(ui.users.search(ui.newGroupWidgets.userSearchEntry.Text))
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
	ui.newGroupWidgets.pendingUsersList.Content = pendingUsersList
	pendingUserHeight := float32(0)
	for i, obj := range pendingUsersList.Objects {
		if i == 6 {
			break
		}
		pendingUserHeight += obj.MinSize().Height
	}
	pendingUserHeight += theme.Padding() * float32(len(pendingUsersList.Objects)+1)
	ui.newGroupWidgets.pendingUsersList.SetMinSize(fyne.Size{Height: pendingUserHeight})
	ui.newGroupWidgets.pendingUsersList.Refresh()
}
