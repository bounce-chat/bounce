package ui

import (
	"errors"
	"io"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type group struct {
	id                               uuid.UUID
	name                             string
	initial                          string
	images                           []uuid.UUID
	users                            *userStore
	admins                           []uuid.UUID
	invites                          []uuid.UUID
	blockedUsers                     []uuid.UUID
	retention                        int64
	restrictUserManagement           bool
	restrictGroupEdits               bool
	restrictPosting                  bool
	overrideReadReceiptSetting       bool
	readReceiptsEnabled              bool
	overrideTypingIndicatorSetting   bool
	typingIndicatorsEnabled          bool
	lastAdminActionMutex             sync.Mutex
	pendingUsers                     *userStore
	notificationsMutedUntil          int64
	createdAt                        int64
	editIcon                         *defaultImage
	headerName                       *widget.Label
	invitesLabel                     *widget.Label
	headerIcon                       *defaultImage
	editContainer                    *fyne.Container
	editThreadNameEntry              *widget.Entry
	retentionSelection               *widget.Select
	clearHistoryButton               *widget.Button
	leaveGroupButton                 *widget.Button
	deleteGroupButton                *widget.Button
	blockGroupButton                 *widget.Button
	addUsersButton                   *widget.Button
	view                             *fyne.Container
	header                           *fyne.Container
	button                           *threadButton
	notificationsEnabledCheck        *widget.Check
	restrictUserManagementCheck      *widget.Check
	restrictGroupEditsCheck          *widget.Check
	restrictPostingCheck             *widget.Check
	readReceiptOverrideSelection     *widget.Select
	typingIndicatorOverrideSelection *widget.Select
	scroll                           *chatHistory //*List // TODO: rename to history
	newUserSearchEntry               *widget.Entry
	availableNewUsersScroll          *container.Scroll
	currentUsersContainer            *container.Scroll
	currentInvitesContainer          *container.Scroll
	pendingUsersContainer            *container.Scroll
	adminChecks                      map[uuid.UUID]*widget.Check
	adminChecksMutex                 sync.Mutex
	removeUserButtons                map[uuid.UUID]*widget.Button
	removeUserButtonsMutex           sync.Mutex
	editUserDialogs                  map[uuid.UUID]dialogWithCallback
	editUserDialogsMutex             sync.Mutex
	typingIndicator                  *typingIndicator
	pendingMessageAttachments        *pendingMessageAttachments
	entry                            *threadEntry
	lastMessage                      int64
	opened                           bool
}

func (g *group) getID() uuid.UUID {
	return g.id
}

func (g *group) getView() *fyne.Container {
	return g.view
}
func (g *group) getEntry() *threadEntry {
	return g.entry
}

func (g *group) chatHistoryScroll() *chatHistory {
	return g.scroll
}

func (g *group) getButton() *threadButton {
	return g.button
}

func (g *group) getTypingIndicator() *typingIndicator {
	return g.typingIndicator
}

func (g *group) getLastMessageTime() int64 {
	return g.lastMessage
}

func (g *group) setLastMessageTime(time int64) {
	g.lastMessage = time
}

func (g *group) getNotificationsMutedUntil() int64 {
	return g.notificationsMutedUntil
}

func (g *group) getEditIcon() *defaultImage {
	return g.editIcon
}

func (g *group) getHeaderIcon() *defaultImage {
	return g.headerIcon
}

func (g *group) isAdmin(userID uuid.UUID) bool {
	for _, id := range g.admins {
		if id == userID {
			return true
		}
	}
	return false
}

func (g *group) setOpened() {
	g.opened = true
}

func (g *group) hasBeenOpened() bool {
	return g.opened
}

func (g *group) getAdminCheck(userID uuid.UUID) *widget.Check {
	g.adminChecksMutex.Lock()
	defer g.adminChecksMutex.Unlock()

	check, ok := g.adminChecks[userID]
	if ok {
		return check
	}

	check = widget.NewCheck("Admin", func(set bool) {})
	check.SetChecked(g.isAdmin(userID))
	g.adminChecks[userID] = check
	return check
}

func (g *group) setInitial() {
	r, n := utf8.DecodeRuneInString(g.name)
	if r == utf8.RuneError {
		log.WithFields(log.Fields{
			"rune_error": r,
			"size":       n,
		}).Error("error setting group inital")
		return
	}
	g.initial = string(r)
}

func (g *group) refreshReadReceiptSettingSelection(options []string) {
	g.readReceiptOverrideSelection.Options = options
	if !g.overrideReadReceiptSetting {
		g.readReceiptOverrideSelection.Selected = options[0]
	} else {
		if g.readReceiptsEnabled {
			g.readReceiptOverrideSelection.Selected = options[1]
		} else {
			g.readReceiptOverrideSelection.Selected = options[2]
		}
	}
	g.readReceiptOverrideSelection.Refresh()
}

func (g *group) refreshTypingIndicatorSettingSelection(options []string) {
	g.typingIndicatorOverrideSelection.Options = options
	if !g.overrideTypingIndicatorSetting {
		g.typingIndicatorOverrideSelection.Selected = options[0]
	} else {
		if g.typingIndicatorsEnabled {
			g.typingIndicatorOverrideSelection.Selected = options[1]
		} else {
			g.typingIndicatorOverrideSelection.Selected = options[2]
		}
	}
	g.typingIndicatorOverrideSelection.Refresh()
}

func (ui *ui) getRemoveUserButton(g *group, userID uuid.UUID) *widget.Button {
	g.removeUserButtonsMutex.Lock()
	defer g.removeUserButtonsMutex.Unlock()

	buttonText := "Remove From Group"
	dialogHeader := "Remove User?"
	dialogPrompt := "Are you sure you want to remove this user from the group?"
	if userID == ui.state.profile.id {
		buttonText = "Leave Group"
		dialogHeader = "Leave Group?"
		dialogPrompt = "Are you sure you want to leave this group?"
	}

	closeEdit := false
	returnToThread := false
	var showError error
	confirmDialogCleanup := func() {
		if closeEdit {
			d, _ := ui.getEditUserDialog(g, userID)
			d.Hide()
		}
		if returnToThread {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
		}
		if showError != nil {
			ui.showDialog(dialog.NewError(showError, ui.window), nil)
		}
	}

	button, ok := g.removeUserButtons[userID]
	if ok {
		return button
	}

	confirmRemoveUser := dialog.NewConfirm(
		dialogHeader,
		dialogPrompt,
		func(confirmed bool) {
			closeEdit = confirmed
			if confirmed {
				returnToThread = ui.state.profile.id == userID
				err := ui.bounce.RemoveUserFromGroup(g.id, userID)
				if err != nil {
					showError = errors.New("error removing user: " + err.Error())
					returnToThread = false
				}
			}
		},
		ui.window,
	)

	button = widget.NewButton(buttonText, func() { ui.showDialog(confirmRemoveUser, confirmDialogCleanup) })
	g.removeUserButtons[userID] = button
	return button
}

func (ui *ui) getEditUserDialog(g *group, userID uuid.UUID) (dialog.Dialog, func()) {
	g.editUserDialogsMutex.Lock()
	g.editUserDialogsMutex.Unlock()

	d, ok := g.editUserDialogs[userID]
	if ok {
		return d.dialog, d.callback
	}

	u, ok := g.users.get(userID)
	if !ok {
		log.WithFields(log.Fields{
			"group_id": g.id,
			"user_id":  userID,
		}).Fatal("cannot create edit user dialog for user not in group")
	}

	var userDetailsDialog dialog.Dialog

	editUserContainer := container.NewVBox(
		container.NewCenter(newDefaultImage(u.id, u.images, u.initials, 64, ui.bounce.GetFileData, nil)), // TODO: get size from theme
		widget.NewButton("Direct Message", func() {
			dm, ok := ui.threads.getDM(u.id)
			if !ok {
				ui.NewDirectMessage(chat.User{
					ID:   u.id,
					Name: u.getDisplayName(),
				})
				dm, ok = ui.threads.getDM(u.id)
				if !ok {
					log.Fatal("DM doesn't exist immediately after creation")
				}
			}

			if fyne.CurrentDevice().IsMobile() {
				userDetailsDialog.Hide() // Close the edit user dialog
				ui.mobileBack()          // Exit the edit group page
				ui.mobileBack()          // Exit the group
				ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeThread, context: dm.user.id})
				ui.state.currentView = viewTypeThread
			} else {
				if userDetailsDialog == nil {
					log.Fatal("userDetailsDialog used before assignment, this should be impossible")
				}
				userDetailsDialog.Hide()
				ui.showMainContainer()
			}
			ui.displayThread(dm)
			ui.bounce.UserConnectionDesired(u.id)
		}),
		ui.getRemoveUserButton(g, u.id),
		g.getAdminCheck(u.id),
	)

	var showError error
	dialogCleanup := func() {
		if showError != nil {
			ui.showDialog(dialog.NewError(showError, ui.window), nil)
		}
	}
	userDetailsDialog = dialog.NewCustomConfirm(u.getDisplayName(), "Apply", "Cancel", editUserContainer, func(apply bool) { // TODO: add listener to update name when it changes
		showError = nil
		if apply {
			if g.getAdminCheck(u.id).Checked && !g.isAdmin(u.id) {
				err := ui.bounce.PromoteGroupAdmin(g.id, u.id)
				if err != nil {
					showError = errors.New("error promoting admin: " + err.Error())
				}
			} else if !g.getAdminCheck(u.id).Checked && g.isAdmin(u.id) {
				err := ui.bounce.DemoteGroupAdmin(g.id, u.id)
				if err != nil {
					showError = errors.New("error demoting admin: " + err.Error())
				}
			}
		}
		g.getAdminCheck(u.id).SetChecked(g.isAdmin(u.id))
	}, ui.window)

	g.editUserDialogs[userID] = dialogWithCallback{dialog: userDetailsDialog, callback: dialogCleanup}

	return userDetailsDialog, dialogCleanup
}

func (ui *ui) addAdmin(g *group, userID uuid.UUID) {
	alreadyAdded := false
	for _, id := range g.admins {
		if id == userID {
			alreadyAdded = true
		}
	}
	if !alreadyAdded {
		g.admins = append(g.admins, userID)
	}

	ui.updateEnabledFeatures(g)
}

func (ui *ui) removeAdmin(g *group, userID uuid.UUID) {
	adminsWithoutUser := []uuid.UUID{}
	for _, id := range g.admins {
		if id != userID {
			adminsWithoutUser = append(adminsWithoutUser, id)
		}
	}
	g.admins = adminsWithoutUser

	ui.updateEnabledFeatures(g)
}

func (ui *ui) amAdmin(g *group) bool {
	for _, id := range g.admins {
		if ui.state.profile.id == id {
			return true
		}
	}

	return false
}

func (ui *ui) OpenNewGroupChat(bounceGroup chat.Group) { // TODO: rename "create and open"?
	fyne.DoAndWait(func() {
		ui.buildNewGroupChat(bounceGroup)

		g, exists := ui.threads.getGroup(bounceGroup.ID)
		if exists {
			ui.showMainContainer()
			ui.displayThread(g)
		} else {
			log.Error("cannot open newly created g because the UI isn't aware of it")
		}
	})
}

func (ui *ui) buildNewGroupChat(bounceGroup chat.Group) {
	if _, exists := ui.threads.getGroup(bounceGroup.ID); exists {
		return
	}

	invitedIDs := []uuid.UUID{}
	for _, u := range bounceGroup.Invites {
		invitedIDs = append(invitedIDs, u.ID)
	}

	g := &group{
		id:                             bounceGroup.ID,
		name:                           bounceGroup.Name,
		images:                         bounceGroup.Images,
		users:                          newUserStore(),
		admins:                         bounceGroup.Admins,
		invites:                        invitedIDs,
		blockedUsers:                   bounceGroup.BlockedUsers,
		retention:                      bounceGroup.Retention,
		overrideReadReceiptSetting:     bounceGroup.OverrideReadReceiptSetting,
		readReceiptsEnabled:            bounceGroup.ReadReceiptsEnabled,
		overrideTypingIndicatorSetting: bounceGroup.OverrideTypingIndicatorSetting,
		typingIndicatorsEnabled:        bounceGroup.TypingIndicatorsEnabled,
		createdAt:                      bounceGroup.CreatedAt,
		editThreadNameEntry:            widget.NewEntry(),
		restrictUserManagement:         bounceGroup.RestrictUserManagement,
		restrictGroupEdits:             bounceGroup.RestrictGroupEdits,
		restrictPosting:                bounceGroup.RestrictPosting,
		notificationsMutedUntil:        bounceGroup.MutedUntil,
		pendingUsers:                   newUserStore(),
		availableNewUsersScroll:        container.NewVScroll(container.NewVBox()),
		currentUsersContainer:          container.NewVScroll(container.NewVBox()),
		currentInvitesContainer:        container.NewVScroll(container.NewVBox()),
		pendingUsersContainer:          container.NewVScroll(container.NewVBox()),
		adminChecks:                    make(map[uuid.UUID]*widget.Check),
		removeUserButtons:              make(map[uuid.UUID]*widget.Button),
		editUserDialogs:                make(map[uuid.UUID]dialogWithCallback),
		lastMessage:                    time.Now().Unix(),
	}
	g.setInitial()

	for _, bu := range bounceGroup.Users {
		u, exists := ui.users.get(bu.ID)
		if !exists {
			u := makeUser(bu)
			ui.users.add(u)
			g.users.add(u)
		} else {
			g.users.add(u)
		}
	}

	g.editThreadNameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		g.editThreadNameEntry.Text = str
		if trimmed != 0 {
			g.editThreadNameEntry.CursorRow = 0
			g.editThreadNameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			g.editThreadNameEntry.Text = string(truncated)

		}
		g.editThreadNameEntry.Refresh()
	}

	g.notificationsEnabledCheck = widget.NewCheck("Enable notifications", func(_ bool) {})
	enabled := g.notificationsMutedUntil != chat.MutedForever
	g.notificationsEnabledCheck.SetChecked(enabled)

	g.restrictUserManagementCheck = widget.NewCheck("Restrict User Management", func(_ bool) {})
	g.restrictGroupEditsCheck = widget.NewCheck("Restrict Group Edits", func(_ bool) {})
	g.restrictPostingCheck = widget.NewCheck("Restrict Posting", func(_ bool) {})
	g.restrictUserManagementCheck.SetChecked(g.restrictUserManagement)
	g.restrictGroupEditsCheck.SetChecked(g.restrictGroupEdits)
	g.restrictPostingCheck.SetChecked(g.restrictPosting)

	ui.buildEditThreadContainer(g)
	editButton := widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() {
		ui.refreshUserSelections(g)
		ui.showEditThreadContainer(g)
	})
	editButton.Importance = widget.LowImportance

	g.headerIcon = newDefaultImage(g.id, bounceGroup.Images, g.initial, 32, ui.bounce.GetFileData, nil) // TODO: get size from theme
	g.headerName = widget.NewLabel(bounceGroup.Name)

	var gLabel *fyne.Container
	if fyne.CurrentDevice().IsMobile() {
		backButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
			ui.mobileBack()
		})
		backButton.Importance = widget.LowImportance
		gLabel = container.NewHBox(
			backButton,
			g.headerIcon,
			g.headerName,
		)
	} else {
		gLabel = container.NewHBox(
			g.headerIcon,
			g.headerName,
		)
	}

	g.headerName.TextStyle = fyne.TextStyle{Bold: true}
	g.header = container.New(
		layout.NewBorderLayout(nil, nil, gLabel, editButton),
		gLabel,
		editButton,
	)

	entry := newThreadEntry(5, func() bool { return len(g.pendingMessageAttachments.files) > 0 })
	g.entry = entry
	entry.OnChanged = func(_ string) {
		go ui.bounce.TypingInGroup(g.id)
	}
	entry.customOnSubmitted = func() {
		gm := chat.GroupMessage{
			Thread: g.id,
			Text:   entry.Text,
		}

		imageAttachments := []chat.ImageAttachment{}
		fileAttachments := []chat.FileAttachment{}
		readers := map[uuid.UUID]io.ReadCloser{}
		sources := map[uuid.UUID]string{}

		pendingAttachments := g.pendingMessageAttachments.extract()
		for _, pma := range pendingAttachments {
			readers[pma.id] = pma.reader
			sources[pma.id] = pma.reader.URI().Path()

			if pma.isImage {
				imageAttachments = append(
					imageAttachments,
					chat.ImageAttachment{
						ID:       pma.id,
						Name:     pma.reader.URI().Name(),
						Size:     pma.fileSize,
						Width:    pma.width,
						Height:   pma.height,
						BlurHash: pma.blurHash,
					},
				)
			} else {
				fileAttachments = append(
					fileAttachments,
					chat.FileAttachment{
						ID:   pma.id,
						Name: pma.reader.URI().Name(),
						Size: pma.fileSize,
					},
				)
			}
		}

		gm.ImageAttachments = imageAttachments
		gm.FileAttachments = fileAttachments
		go ui.bounce.SendGroupMessage(gm, readers, sources)
	}

	openThread := func() {
		ui.displayThread(g)
		ui.bounce.GroupConnectionDesired(g.id)
		if fyne.CurrentDevice().IsMobile() {
			ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeThread, context: g.id})
		}
		ui.state.currentView = viewTypeThread
	}
	g.button = newThreadButton(newDefaultImage(g.id, g.images, g.initial, 64, ui.bounce.GetFileData, openThread), g.name, openThread)
	g.scroll = ui.newChatHistory(g)

	// Keep the last message time counter up to date
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			fyne.Do(func() { g.button.updateLastMessageTimeText() })
		}
	}()

	g.typingIndicator = newTypingIndicator(typingIndicatorModeIcons, ui.bounce.GetFileData)
	g.typingIndicator.Hide()

	addFiles := widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() {
		dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if reader == nil {
				return
			}

			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Debug("error selecting file for message attachment")
				return
			}

			err = g.pendingMessageAttachments.add(reader)
			if err != nil {
				ui.showDialog(dialog.NewError(err, ui.window), nil)
			}
		}, ui.window).Show() // We do not use showDialog here because on mobile this uses a native intent
	})
	addFiles.Importance = widget.LowImportance
	g.pendingMessageAttachments = newPendingMessageAttachments()
	footer := container.NewVBox(
		g.typingIndicator,
		g.pendingMessageAttachments,
		container.New(
			layout.NewBorderLayout(nil, nil, nil, addFiles),
			addFiles,
			g.entry,
		),
	)
	g.view = container.New(
		layout.NewBorderLayout(g.header, footer, nil, nil),
		g.header,
		footer,
		container.New(&autoscrollLayout{}, g.scroll),
	)
	ui.threads.add(g.id, g)
	ui.refreshThreadOrder()
	ui.updateEnabledFeatures(g)

	ti, err := ui.newGroupCreated(g.id, g.id, bounceGroup.CreatedBy, bounceGroup.CreatedAt)
	if err != nil {
		log.WithFields(log.Fields{
			"error":      err.Error(),
			"g":          g.id,
			"created_by": bounceGroup.CreatedBy,
		}).Warn("error creating thread item for g creation")
	} else {
		ui.appendThreadItem(g, ti)
		ti.setButton(g.button)
	}
}

func (ui *ui) SetGroupState(bounceGroup chat.Group) {
	member := false
	for _, bu := range bounceGroup.Users {
		if ui.state.profile.id == bu.ID {
			member = true
			break
		}
	}
	invited := false
	for _, invitedUser := range bounceGroup.Invites {
		if ui.state.profile.id == invitedUser.ID {
			invited = true
			break
		}
	}
	if !member && !invited {
		log.WithFields(log.Fields{
			"group_id": bounceGroup.ID,
		}).Warn("cannot display group that we are neither in nor invited to")
		return
	}

	if member && invited {
		log.WithFields(log.Fields{
			"group_id": bounceGroup.ID,
		}).Warn("cannot display group that we are both in and invited to")
		return
	}

	if !member && invited {
		// If the thread is already being displayed as a real thread, remove it before
		// creating the invite
		t, exists := ui.threads.get(bounceGroup.ID)
		if exists {
			if _, ok := t.(*invite); !ok {
				ui.threads.remove(bounceGroup.ID)
			}
		}

		fyne.DoAndWait(func() {
			t, exists := ui.threads.get(bounceGroup.ID)
			if !exists {
				ui.buildNewGroupChatInvite(bounceGroup)
				t, exists = ui.threads.get(bounceGroup.ID)
				if !exists {
					log.WithFields(log.Fields{
						"group_id": bounceGroup.ID,
					}).Warn("cannot set state on group that could not be created")
					return
				}
			}

			i, ok := t.(*invite)
			if !ok {
				log.Error("cannot cast recently created invite thread to invite")
				return
			}

			i.invitedBy = bounceGroup.InvitedBy
			i.timestamp = bounceGroup.InvitedAt
			ui.refreshInvitedBy(i)

			i.images = bounceGroup.Images
			i.icon.images = bounceGroup.Images
			i.icon.Refresh()

			i.name = bounceGroup.Name
			i.nameLabel.Segments[0].(*widget.TextSegment).Text = bounceGroup.Name
			i.nameLabel.Refresh()

			members := []uuid.UUID{}
			for _, bu := range bounceGroup.Users {
				u, ok := ui.users.get(bu.ID)
				if !ok {
					u = makeUser(bu)
				}
				ui.users.add(u)
				members = append(members, bu.ID)
			}
			i.members = members
			i.admins = bounceGroup.Admins

			invites := []uuid.UUID{}
			for _, bu := range bounceGroup.Invites {
				u, ok := ui.users.get(bu.ID)
				if !ok {
					u = makeUser(bu)
				}
				ui.users.add(u)
				invites = append(invites, bu.ID)
			}
			i.invited = invites
			ui.refreshInviteUsers(i)

			i.setInitial()
			i.button.setName(bounceGroup.Name, i.initial)
			i.button.threadImage.images = bounceGroup.Images
			i.button.threadImage.Refresh()
		})

		return
	}

	// If we are displaying an invite, and we are now a member of this group, remove the invite from the list of threads
	// before creating the actual thread
	wasAnInvite := false
	if member && !invited {
		g, exists := ui.threads.get(bounceGroup.ID)
		if exists {
			if _, ok := g.(*invite); ok {
				ui.threads.remove(bounceGroup.ID)
				wasAnInvite = true
			}
		}
	}

	fyne.DoAndWait(func() {
		g, exists := ui.threads.getGroup(bounceGroup.ID)
		if !exists {
			ui.buildNewGroupChat(bounceGroup)
			g, exists = ui.threads.getGroup(bounceGroup.ID)
			if !exists {
				log.WithFields(log.Fields{
					"group_id": bounceGroup.ID,
				}).Warn("cannot set state on group that could not be created")
				return
			}
		}

		g.name = bounceGroup.Name
		g.headerName.Text = bounceGroup.Name
		g.headerName.Refresh()
		g.setInitial()
		g.headerIcon.setString(g.initial)
		g.editIcon.setString(g.initial)
		g.images = bounceGroup.Images
		g.editIcon.images = bounceGroup.Images
		g.editIcon.Refresh()
		g.headerIcon.images = bounceGroup.Images
		g.headerIcon.Refresh()
		g.button.setName(bounceGroup.Name, g.initial)
		g.button.threadImage.images = bounceGroup.Images
		g.button.threadImage.Refresh()

		g.users.empty()
		for _, bu := range bounceGroup.Users {
			u, ok := ui.users.get(bu.ID)
			if !ok {
				u = makeUser(bu)
			}
			ui.users.add(u)
			g.users.add(u)
			g.pendingUsers.remove(u.id)
		}

		g.admins = bounceGroup.Admins

		for _, u := range g.users.userList {
			g.getAdminCheck(u.id).SetChecked(g.isAdmin(u.id))
			g.getAdminCheck(u.id).Refresh()
		}

		invites := []uuid.UUID{}
		for _, bu := range bounceGroup.Invites {
			u, ok := ui.users.get(bu.ID)
			if !ok {
				u = makeUser(bu)
			}
			ui.users.add(u)
			invites = append(invites, bu.ID)
		}
		g.invites = invites

		g.blockedUsers = bounceGroup.BlockedUsers

		g.retention = bounceGroup.Retention
		g.retentionSelection.Selected = getRetentionName(bounceGroup.Retention)
		g.retentionSelection.Refresh()

		g.notificationsMutedUntil = bounceGroup.MutedUntil
		enabled := g.notificationsMutedUntil != chat.MutedForever
		g.notificationsEnabledCheck.SetChecked(enabled)
		g.notificationsEnabledCheck.Refresh()

		g.restrictUserManagementCheck.SetChecked(bounceGroup.RestrictUserManagement)
		g.restrictUserManagement = bounceGroup.RestrictUserManagement

		g.restrictGroupEditsCheck.SetChecked(bounceGroup.RestrictGroupEdits)
		g.restrictGroupEdits = bounceGroup.RestrictGroupEdits

		g.restrictPostingCheck.SetChecked(bounceGroup.RestrictPosting)
		g.restrictPosting = bounceGroup.RestrictPosting

		ui.updateEnabledFeatures(g)
		ui.refreshUserSelections(g)

		g.overrideReadReceiptSetting = bounceGroup.OverrideReadReceiptSetting
		g.readReceiptsEnabled = bounceGroup.ReadReceiptsEnabled
		g.refreshReadReceiptSettingSelection(ui.readReceiptOverrideSelectionOptions())
		g.overrideTypingIndicatorSetting = bounceGroup.OverrideTypingIndicatorSetting
		g.typingIndicatorsEnabled = bounceGroup.TypingIndicatorsEnabled
		g.refreshTypingIndicatorSettingSelection(ui.typingIndicatorOverrideSelectionOptions())

		g.editThreadNameEntry.Text = bounceGroup.Name
		g.editThreadNameEntry.Refresh()

		if wasAnInvite {
			if ui.state.activeThread == g.id {
				ui.displayThread(g)
			}
		}
	})
}

func (ui *ui) NotifyInvitedToGroup(name string) {
	deferToAnotherDevice := !ui.state.active && ui.state.anotherDeviceActive
	if !deferToAnotherDevice {
		ui.app.SendNotification(fyne.NewNotification(name, "You have been invited to a group"))
	}
}

func (ui *ui) NotifyAddedToGroup(name string) {
	deferToAnotherDevice := !ui.state.active && ui.state.anotherDeviceActive
	if !deferToAnotherDevice {
		ui.app.SendNotification(fyne.NewNotification(name, "You have been added to a group"))
	}
}

func (ui *ui) DisplayGroupMessage(gm chat.GroupMessage) {
	g, exists := ui.threads.getGroup(gm.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": gm.Thread,
		}).Error("group not found for group message")
		return
	}

	ti, err := ui.newGroupMessage(gm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for group message")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) DisplaySentGroupMessage(gm chat.GroupMessage) {
	g, exists := ui.threads.getGroup(gm.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": gm.Thread,
		}).Error("group not found for group message")
		return
	}

	ti, err := ui.newGroupMessage(gm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for group message")
		return
	}

	fyne.DoAndWait(func() {
		ui.appendThreadItem(g, ti)

		g.getEntry().Text = ""
		g.getEntry().Refresh()

		g.chatHistoryScroll().ScrollToBottom()
		ui.containers.chat.Refresh()
	})
}

func (ui *ui) InviteUser(ugiu chat.UpdateGroupInviteUser) {
	g, exists := ui.threads.getGroup(ugiu.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugiu.Thread,
		}).Error("group not found for update group add user")
		return
	}

	ti, err := ui.newUpdateGroupInviteUser(ugiu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for adding user to group")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) RemoveUser(ugru chat.UpdateGroupRemoveUser) {
	g, exists := ui.threads.getGroup(ugru.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugru.Thread,
		}).Error("cannot remove user from unknown group")
		return
	}

	ti, err := ui.newUpdateGroupRemoveUser(ugru)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for removing user to group")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) RemovedFromGroup(rfg chat.RemovedFromGroup) {
	fyne.DoAndWait(func() {
		if ui.state.activeThread == rfg.Group {
			if fyne.CurrentDevice().IsMobile() {
				ui.showMainContainer()
			} else {
				ui.containers.chat.Objects = []fyne.CanvasObject{ui.containers.defaultContainer}
				ui.containers.chat.Refresh()
				ui.state.activeThread = uuid.Nil
			}
		}
		if rfg.Actor != ui.state.profile.id {
			var actorName string
			actor, ok := ui.users.get(rfg.Actor)
			if ok {
				actorName = actor.getDisplayName()
			} else {
				log.WithFields(log.Fields{
					"actor": rfg.Actor,
					"group": rfg.Group,
				}).Error("unknown user removed us from a group")
				actorName = "unknown user"
			}
			t, ok := ui.threads.get(rfg.Group)
			if !ok {
				log.WithFields(log.Fields{
					"group": rfg.Group,
				}).Error("removed from unknown group")
				return
			}
			g, ok := t.(*group)
			if ok {
				if !ui.state.initialSyncIncomplete {
					ui.showDialog(dialog.NewInformation("Removed From Group", actorName+" removed you from "+g.name, ui.window), nil)
				}
			} else {
				i, ok := t.(*invite)
				if ok {
					if !ui.state.initialSyncIncomplete {
						ui.showDialog(dialog.NewInformation("Removed From Group", actorName+" removed your invite to "+i.name, ui.window), nil)
					}
				} else {
					log.WithFields(log.Fields{
						"group": rfg.Group,
					}).Error("removed from group applied to thread that is not group or invite")
				}
			}
		}
		ui.threads.remove(rfg.Group)
		ui.refreshThreadOrder()
	})
}

func (ui *ui) GroupDeleted(gd chat.GroupDeleted) {
	fyne.DoAndWait(func() {
		if ui.state.activeThread == gd.Group {
			if fyne.CurrentDevice().IsMobile() {
				ui.showMainContainer()
			} else {
				ui.containers.chat.Objects = []fyne.CanvasObject{ui.containers.defaultContainer}
				ui.containers.chat.Refresh()
				ui.state.activeThread = uuid.Nil
			}
		}

		if gd.Actor != ui.state.profile.id {
			var actorName string
			if gd.Actor == ui.state.profile.id {
				actorName = "You"
			} else {
				actor, ok := ui.users.get(gd.Actor)
				if ok {
					actorName = actor.getDisplayName()
				} else {
					log.WithFields(log.Fields{
						"actor": gd.Actor,
						"group": gd.Group,
					}).Warn("unknown user deleted group")
					actorName = "unknown user"
				}
			}

			t, ok := ui.threads.get(gd.Group)
			if !ok {
				log.WithFields(log.Fields{
					"group": gd.Group,
				}).Error("unknown group deleted")
				return
			}
			g, ok := t.(*group)
			if !ok {
				log.WithFields(log.Fields{
					"group": gd.Group,
				}).Error("delete group applied tp thread that is not group")
				return
			}
			if !ui.state.initialSyncIncomplete {
				ui.showDialog(dialog.NewInformation("Group Deleted", actorName+" deleted the group \""+g.name+"\"", ui.window), nil)
			}
		}

		ui.threads.remove(gd.Group)
		ui.refreshThreadOrder()
	})
}

func (ui *ui) RenameGroup(ugn chat.UpdateGroupName) {
	g, exists := ui.threads.getGroup(ugn.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugn.Thread,
		}).Error("cannot update name for unknown group")
		return
	}

	ti, err := ui.newUpdateGroupName(ugn)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for updating group name")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) GroupRetentionChanged(ugr chat.UpdateGroupRetention) {
	g, exists := ui.threads.getGroup(ugr.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugr.Thread,
		}).Error("cannot update retention for unknown group")
		return
	}

	ti, err := ui.newUpdateGroupRetention(ugr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for updating group retention")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) GroupChatHistoryCleared(ugch chat.UpdateGroupClearHistory) {
	g, exists := ui.threads.getGroup(ugch.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugch.Thread,
		}).Error("cannot clear history for unknown group")
		return
	}

	ti, err := ui.newUpdateGroupClearHistory(ugch)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for clearing group history")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })

	// TODO: it's possible we cleared messages and there are messages newer than the clear time
	// that we want to preserve.  In that case, these messages will not have been removed from the
	// history scroll, and we should insert the message about the clearing above them, so it makes
	// sense.  This will require we know the timestamp of the clearing update in the frontend,
	// in fact everything that goes into a thread should follow an interface that has timestamps.
	// once that's done we can insertion sort these in.
}

func (ui *ui) AdminPromoted(ugap chat.UpdateGroupAdminPromoted) {
	g, exists := ui.threads.getGroup(ugap.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugap.Thread,
		}).Error("cannot promote admin for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupAdminPromoted(ugap)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group admin promoted")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) AdminDemoted(ugad chat.UpdateGroupAdminDemoted) {
	g, exists := ui.threads.getGroup(ugad.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugad.Thread,
		}).Error("cannot demote admin for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupAdminDemoted(ugad)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group admin demoted")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) UserManagementRestricted(ugumr chat.UpdateGroupUserManagementRestricted) {
	g, exists := ui.threads.getGroup(ugumr.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugumr.Thread,
		}).Error("cannot restrict user management for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupUserManagementRestricted(ugumr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group user management restricted")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) UserManagementUnrestricted(ugumu chat.UpdateGroupUserManagementUnrestricted) {
	g, exists := ui.threads.getGroup(ugumu.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugumu.Thread,
		}).Error("cannot unrestrict user management for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupUserManagementUnrestricted(ugumu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group user management unrestricted")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) GroupEditsRestricted(uger chat.UpdateGroupEditsRestricted) {
	g, exists := ui.threads.getGroup(uger.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": uger.Thread,
		}).Error("cannot restrict edits for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupEditsRestricted(uger)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group edits restricted")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) GroupEditsUnrestricted(ugeu chat.UpdateGroupEditsUnrestricted) {
	g, exists := ui.threads.getGroup(ugeu.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugeu.Thread,
		}).Error("cannot unrestrict edits for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupEditsUnrestricted(ugeu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group edits unrestricted")
		return

	}
	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) PostingRestricted(ugpr chat.UpdateGroupPostingRestricted) {
	g, exists := ui.threads.getGroup(ugpr.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugpr.Thread,
		}).Error("cannot restrict posting for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupPostingRestricted(ugpr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group posting restricted")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) PostingUnrestricted(ugpu chat.UpdateGroupPostingUnrestricted) {
	g, exists := ui.threads.getGroup(ugpu.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugpu.Thread,
		}).Error("cannot unrestrict posting for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupPostingUnrestricted(ugpu)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group posting unrestricted")
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) UserBlockedGroup(ubg chat.UserBlockedGroup) {
	g, exists := ui.threads.getGroup(ubg.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ubg.Thread,
		}).Error("cannot display block for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupUserBlocked(ubg)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for user blocked group")
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) UserChangedGroupImage(ugci chat.UpdateGroupUserChangedGroupImage) {
	g, exists := ui.threads.getGroup(ugci.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugci.Thread,
		}).Error("cannot display block for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupUserChangedGroupImage(ugci)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for user changed group image")
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) GroupInviteRevoked(ugri chat.UpdateGroupInviteRevoked) {
	g, exists := ui.threads.getGroup(ugri.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugri.Thread,
		}).Error("cannot revoke invite for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupInviteRevoked(ugri)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group revoke invite")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) GroupInviteAccepted(ugia chat.UpdateGroupInviteAccepted) {
	g, exists := ui.threads.getGroup(ugia.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugia.Thread,
		}).Error("cannot accept invite for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupInviteAccepted(ugia)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group invite accepted")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) GroupInviteRejected(ugir chat.UpdateGroupInviteRejected) {
	g, exists := ui.threads.getGroup(ugir.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugir.Thread,
		}).Error("cannot reject invite for group that doesn't exist")
		return
	}

	ti, err := ui.newUpdateGroupInviteRejected(ugir)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update group invite rejected")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) RollbackGroup(groupID uuid.UUID) {
	ui.threads.remove(groupID)
	ui.refreshThreadOrder()
}

func (ui *ui) updateEnabledFeatures(g *group) {
	amAdmin := g.isAdmin(ui.state.profile.id)

	if amAdmin {
		g.restrictUserManagementCheck.Enable()
		g.restrictGroupEditsCheck.Enable()
		g.restrictPostingCheck.Enable()
		g.deleteGroupButton.Enable()

		g.adminChecksMutex.Lock()
		for _, check := range g.adminChecks {
			check.Enable()
		}
		g.adminChecksMutex.Unlock()
	} else {
		g.restrictUserManagementCheck.Disable()
		g.restrictGroupEditsCheck.Disable()
		g.restrictPostingCheck.Disable()
		g.deleteGroupButton.Disable()

		if len(g.admins) > 0 {
			g.adminChecksMutex.Lock()
			for _, check := range g.adminChecks {
				check.Disable()
			}
			g.adminChecksMutex.Unlock()
		} else {
			g.adminChecksMutex.Lock()
			for _, check := range g.adminChecks {
				check.Enable()
			}
			g.adminChecksMutex.Unlock()
		}
	}

	if g.restrictUserManagement && !amAdmin {
		g.addUsersButton.Disable()

		g.removeUserButtonsMutex.Lock()
		for _, button := range g.removeUserButtons {
			button.Disable()
		}
		g.removeUserButtonsMutex.Unlock()
	} else {
		g.addUsersButton.Enable()

		g.removeUserButtonsMutex.Lock()
		for _, button := range g.removeUserButtons {
			button.Enable()
		}
		g.removeUserButtonsMutex.Unlock()
	}

	if g.restrictGroupEdits && !amAdmin {
		g.editThreadNameEntry.Disable()
		g.retentionSelection.Disable()
		g.clearHistoryButton.Disable()
	} else {
		g.editThreadNameEntry.Enable()
		g.retentionSelection.Enable()
		g.clearHistoryButton.Enable()
	}

	if g.restrictPosting && !amAdmin {
		g.entry.Disable()
	} else {
		g.entry.Enable()
	}
}
