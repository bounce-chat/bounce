package ui

import (
	"errors"
	"io"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/bounce-chat/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type newGroup struct {
	icon                          *defaultImage
	iconData                      []byte
	iconName                      string
	nameEntry                     *widget.Entry
	selectedUsersContainer        *container.Scroll
	pendingUsersList              *container.Scroll
	allAvailableUsers             *container.Scroll
	userSearchEntry               *widget.Entry
	selectedUsers                 *userStore
	pendingUsers                  *userStore
	createButton                  *widget.Button
	addAdminsButton               *widget.Button
	addUsersButton                *widget.Button
	retentionSelection            *widget.Select
	userManagementRestrictedCheck *widget.Check
	groupEditsRestrictedCheck     *widget.Check
	postingRestrictedCheck        *widget.Check
}

func (ui *ui) buildNewGroup() {
	ui.widgets.newGroup = &newGroup{
		nameEntry:              widget.NewEntry(),
		iconData:               []byte{},
		selectedUsersContainer: container.NewVScroll(container.NewVBox()),
		allAvailableUsers:      container.NewVScroll(container.NewVBox()),
		pendingUsersList:       container.NewVScroll(container.NewVBox()),
		selectedUsers:          newUserStore(),
		pendingUsers:           newUserStore(),
	}

	ui.widgets.newGroup.userSearchEntry = widget.NewEntry()
	ui.widgets.newGroup.userSearchEntry.OnChanged = func(str string) {
		ui.refreshNewGroupUserSelections(ui.users.search(str))
	}
	newUserSelector := container.New(
		layout.NewBorderLayout(ui.widgets.newGroup.userSearchEntry, nil, nil, nil),
		ui.widgets.newGroup.userSearchEntry,
		container.NewVBox(
			widget.NewLabel("Users to add:"),
			ui.widgets.newGroup.selectedUsersContainer,
			widget.NewLabel("All Users:"),
			ui.widgets.newGroup.allAvailableUsers,
		),
	)
	addUsersDialogCleanup := func() {
		ui.widgets.newGroup.selectedUsers.empty()
		ui.widgets.newGroup.userSearchEntry.Text = ""
		ui.widgets.newGroup.userSearchEntry.Refresh()
		ui.refreshNewGroupUserSelections(ui.users.search(""))
	}
	addUsersDialog := dialog.NewCustomConfirm("Add Users", "Save", "Cancel", newUserSelector, func(apply bool) {
		if apply {
			for _, u := range ui.widgets.newGroup.selectedUsers.userList {
				ui.widgets.newGroup.pendingUsers.add(u)
			}
		}
	}, ui.window)

	ui.widgets.newGroup.addUsersButton = widget.NewButton("Add Users", func() {
		ui.showDialog(addUsersDialog, addUsersDialogCleanup)
	})
	ui.widgets.newGroup.retentionSelection = widget.NewSelect(retentionSelections, nil)
	ui.widgets.newGroup.retentionSelection.Selected = getRetentionName(ui.state.settings.DefaultGroupRetention)
	ui.widgets.newGroup.userManagementRestrictedCheck = widget.NewCheck("Restrict User Management", func(_ bool) {})
	ui.widgets.newGroup.userManagementRestrictedCheck.SetChecked(ui.state.settings.NewGroupRestrictUserManagement)
	ui.widgets.newGroup.groupEditsRestrictedCheck = widget.NewCheck("Restrict Group Edits", func(_ bool) {})
	ui.widgets.newGroup.groupEditsRestrictedCheck.SetChecked(ui.state.settings.NewGroupRestrictGroupEdits)
	ui.widgets.newGroup.postingRestrictedCheck = widget.NewCheck("Restrict Posting", func(_ bool) {})
	ui.widgets.newGroup.postingRestrictedCheck.SetChecked(ui.state.settings.NewGroupRestrictPosting)

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

	ui.widgets.newGroup.createButton = widget.NewButton("Create", func() {
		ui.widgets.newGroup.createButton.Disable()
		ui.widgets.newGroup.nameEntry.Disable()

		selectedRetentionString := ui.widgets.newGroup.retentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			ui.showDialog(dialog.NewError(errors.New("invalid retention selection: "+selectedRetentionString), ui.window), nil)
		}

		initialInvites := []uuid.UUID{}
		for _, user := range ui.widgets.newGroup.pendingUsers.alphabetized() {
			initialInvites = append(initialInvites, user.id)
		}

		newGroup := chat.NewGroup{
			Name:                   strings.TrimSpace(ui.widgets.newGroup.nameEntry.Text),
			Image:                  ui.widgets.newGroup.iconData,
			InitialInvites:         initialInvites,
			Retention:              selectedRetentionValue,
			RestrictUserManagement: ui.widgets.newGroup.userManagementRestrictedCheck.Checked,
			RestrictGroupEdits:     ui.widgets.newGroup.groupEditsRestrictedCheck.Checked,
			RestrictPosting:        ui.widgets.newGroup.postingRestrictedCheck.Checked,
		}

		err := ui.bounce.CreateGroup(newGroup)
		if err != nil {
			ui.showDialog(dialog.NewError(errors.New("Error creating group: "+err.Error()), ui.window), nil)
		}
		ui.widgets.newGroup.createButton.Enable()
		ui.widgets.newGroup.nameEntry.Enable()
	})
	ui.widgets.newGroup.createButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
		}
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, ui.widgets.newGroup.createButton),
		ui.widgets.newGroup.createButton,
		cancelButton,
	)

	initial := ""
	ui.widgets.newGroup.nameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		ui.widgets.newGroup.nameEntry.Text = str
		if trimmed != 0 {
			ui.widgets.newGroup.nameEntry.CursorRow = 0
			ui.widgets.newGroup.nameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			initial = string(truncated)

		}
		ui.widgets.newGroup.nameEntry.Refresh()

		// Set the group icon
		r, _ := utf8.DecodeRuneInString(str)
		if r == utf8.RuneError {
			initial = ""
		} else {
			initial = string(r)
		}

		ui.widgets.newGroup.icon.setString(initial)
	}
	fileGetter := func(_ uuid.UUID) ([]byte, error) {
		return ui.widgets.newGroup.iconData, nil
	}
	ui.widgets.newGroup.icon = newDefaultImage(uuid.Nil, []uuid.UUID{}, initial, 128, fileGetter, func() {
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

			ui.widgets.newGroup.iconData = data
			ui.widgets.newGroup.icon.images = []uuid.UUID{uuid.New()}
			ui.widgets.newGroup.icon.setBackground()
			ui.widgets.newGroup.icon.Refresh()
		}, ui.window).Show() // We do not use showDialog here because on mobile this uses a native intent
	})

	ui.views.newGroup = container.New(
		layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
		closeBar,
		actionButtons,
		container.NewVBox(
			container.NewCenter(ui.widgets.newGroup.icon),
			ui.widgets.newGroup.nameEntry,
			widget.NewLabel("Users:"),
			ui.widgets.newGroup.pendingUsersList,
			container.NewHBox(ui.widgets.newGroup.addUsersButton),
			widget.NewAccordion(
				&widget.AccordionItem{
					Title: "Advanced Options",
					Detail: container.NewVBox(
						widget.NewLabel("Disappearing Messages"),
						ui.widgets.newGroup.retentionSelection,
						widget.NewLabel("Permissions"),
						ui.widgets.newGroup.userManagementRestrictedCheck,
						ui.widgets.newGroup.groupEditsRestrictedCheck,
						ui.widgets.newGroup.postingRestrictedCheck,
					),
				},
			),
		),
	)
}

func (ui *ui) showNewGroup() {
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeNewGroup})
	}
	ui.state.currentView = viewTypeNewGroup
	ui.clearNewGroupSelectors()
	ui.window.SetContent(ui.views.newGroup)
	ui.widgets.newGroup.iconData = []byte{}
	ui.widgets.newGroup.icon.images = []uuid.UUID{}
	ui.widgets.newGroup.icon.setBackground()
	ui.widgets.newGroup.icon.Refresh()
	if !fyne.CurrentDevice().IsMobile() {
		ui.window.Canvas().Focus(ui.widgets.newGroup.nameEntry)
	}
}

func (ui *ui) clearNewGroupSelectors() {
	ui.widgets.newGroup.createButton.Enable()
	ui.widgets.newGroup.nameEntry.Enable()

	ui.widgets.newGroup.icon.setString("")
	ui.widgets.newGroup.iconData = []byte{}
	ui.widgets.newGroup.icon.images = []uuid.UUID{}
	ui.widgets.newGroup.icon.Refresh()

	ui.widgets.newGroup.nameEntry.Text = ""
	ui.widgets.newGroup.nameEntry.Refresh()

	ui.widgets.newGroup.userSearchEntry.Text = ""
	ui.widgets.newGroup.userSearchEntry.Refresh()

	ui.widgets.newGroup.selectedUsers.empty()
	ui.widgets.newGroup.pendingUsers.empty()
	ui.refreshNewGroupUserSelections(ui.users.search(ui.widgets.newGroup.userSearchEntry.Text))

	ui.widgets.newGroup.retentionSelection.Selected = getRetentionName(ui.state.settings.DefaultGroupRetention)
	ui.widgets.newGroup.userManagementRestrictedCheck.SetChecked(ui.state.settings.NewGroupRestrictUserManagement)
	ui.widgets.newGroup.groupEditsRestrictedCheck.SetChecked(ui.state.settings.NewGroupRestrictGroupEdits)
	ui.widgets.newGroup.postingRestrictedCheck.SetChecked(ui.state.settings.NewGroupRestrictPosting)
}

func (ui *ui) refreshNewGroupUserSelections(allAvailableUsers []*user) {
	// Add the currently selected users to the display
	currentUsersList := container.NewVBox()
	for _, thisUser := range ui.widgets.newGroup.selectedUsers.alphabetized() {
		func(u *user) {
			removePendingUserButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize(), ui.bounce.GetFileData, nil),
				u.getDisplayName(),
				false,
				func() {
					ui.widgets.newGroup.selectedUsers.remove(u.id)
					ui.refreshNewGroupUserSelections(ui.users.search(ui.widgets.newGroup.userSearchEntry.Text))
				},
			)
			currentUsersList.Objects = append(
				currentUsersList.Objects,
				removePendingUserButton,
			)
		}(thisUser)
	}
	ui.widgets.newGroup.selectedUsersContainer.Content = currentUsersList
	selectedUserHeight := float32(0)
	for i, obj := range currentUsersList.Objects {
		if i == 3 {
			break
		}
		selectedUserHeight += obj.MinSize().Height
	}
	selectedUserHeight += theme.Padding() * float32(len(currentUsersList.Objects)+1)
	ui.widgets.newGroup.selectedUsersContainer.SetMinSize(fyne.Size{Height: selectedUserHeight})
	ui.widgets.newGroup.selectedUsersContainer.Refresh()

	// Update the available user to exclude these pending users
	allUsersListBox := container.NewVBox()
	for _, thisUser := range allAvailableUsers {
		// Exclude users that are pending addition to the group
		if _, exists := ui.widgets.newGroup.selectedUsers.get(thisUser.id); exists {
			continue
		}
		if _, exists := ui.widgets.newGroup.pendingUsers.get(thisUser.id); exists {
			continue
		}
		// Exclude blocked users
		if thisUser.blocked {
			continue
		}
		// Exclude myself
		if thisUser.id == ui.state.profile.id {
			continue
		}

		func(u *user) {
			addUserButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize(), ui.bounce.GetFileData, nil),
				u.getDisplayName(),
				false,
				func() {
					ui.widgets.newGroup.selectedUsers.add(u)
					ui.refreshNewGroupUserSelections(ui.users.search(ui.widgets.newGroup.userSearchEntry.Text))
				},
			)
			allUsersListBox.Objects = append(
				allUsersListBox.Objects,
				addUserButton,
			)
		}(thisUser)
	}
	ui.widgets.newGroup.allAvailableUsers.Content = allUsersListBox
	availableUserHeight := float32(0)
	for i, obj := range allUsersListBox.Objects {
		if i == 3 {
			break
		}
		availableUserHeight += obj.MinSize().Height
	}
	availableUserHeight += theme.Padding() * float32(len(allUsersListBox.Objects)+1)
	ui.widgets.newGroup.allAvailableUsers.SetMinSize(fyne.Size{Height: availableUserHeight})
	ui.widgets.newGroup.allAvailableUsers.Refresh()

	// Refresh the users that have been selected for the new group
	myIcon := newDefaultImage(ui.state.profile.id, ui.state.profile.images, ui.state.profile.initials, theme.IconInlineSize()*2, ui.bounce.GetFileData, nil)
	myName := widget.NewLabel(ui.state.profile.name)

	optionButtons := container.NewHBox(widget.NewLabel("Admin"))
	pendingUsersList := container.NewVBox(container.New(
		layout.NewBorderLayout(nil, nil, myIcon, optionButtons),
		myIcon,
		optionButtons,
		myName,
	))
	for _, thisUser := range ui.widgets.newGroup.pendingUsers.alphabetized() {
		func(u *user) {
			userIcon := newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, ui.bounce.GetFileData, nil)
			userName := widget.NewLabel(u.getDisplayName())
			userName.Truncation = fyne.TextTruncateEllipsis

			removeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
				ui.widgets.newGroup.pendingUsers.remove(u.id)
				ui.refreshNewGroupUserSelections(ui.users.search(ui.widgets.newGroup.userSearchEntry.Text))
			})
			removeButton.Importance = widget.LowImportance
			optionButtons := container.NewHBox(
				removeButton,
			)
			pendingUserRow := container.New(
				layout.NewBorderLayout(nil, nil, userIcon, optionButtons),
				userIcon,
				optionButtons,
				userName,
			)

			pendingUsersList.Objects = append(
				pendingUsersList.Objects,
				pendingUserRow,
			)
		}(thisUser)
	}
	ui.widgets.newGroup.pendingUsersList.Content = pendingUsersList
	pendingUserHeight := float32(0)
	for i, obj := range pendingUsersList.Objects {
		if i == 6 {
			break
		}
		pendingUserHeight += obj.MinSize().Height
	}
	pendingUserHeight += theme.Padding() * float32(len(pendingUsersList.Objects)+1)
	ui.widgets.newGroup.pendingUsersList.SetMinSize(fyne.Size{Height: pendingUserHeight})
	ui.widgets.newGroup.pendingUsersList.Refresh()
}
