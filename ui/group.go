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
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type group struct {
	id                               uuid.UUID
	name                             binding.String
	initial                          binding.String
	images                           []uuid.UUID
	users                            *userStore
	admins                           []uuid.UUID
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

func (g *group) isAdmin(userID uuid.UUID) bool {
	for _, id := range g.admins {
		if id == userID {
			return true
		}
	}
	return false
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
	name, err := g.name.Get()
	if err != nil {
		log.Fatal(err.Error())
	}
	r, n := utf8.DecodeRuneInString(name)
	if r == utf8.RuneError {
		log.WithFields(log.Fields{
			"rune_error": r,
			"size":       n,
		}).Error("error setting group inital")
		return
	}
	g.initial.Set(string(r))
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
	if userID == ui.profile.id {
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
			ui.showDialog(dialog.NewError(showError, ui.mainWindow), nil)
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
				returnToThread = ui.profile.id == userID
				err := ui.bounce.RemoveUserFromGroup(g.id, userID)
				if err != nil {
					showError = errors.New("error removing user: " + err.Error())
					returnToThread = false
				}
			}
		},
		ui.mainWindow,
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
			dm, dmExists := ui.dms[u.id]
			if !dmExists {
				ui.NewDirectMessage(chat.User{
					ID:   u.id,
					Name: u.getName(),
				})
				dm, dmExists = ui.dms[u.id]
				if !dmExists {
					log.Fatal("DM doesn't exist immediately after creation")
				}
			}

			if fyne.CurrentDevice().IsMobile() {
				userDetailsDialog.Hide() // Close the edit user dialog
				ui.mobileBack()          // Exit the edit group page
				ui.mobileBack()          // Exit the group
				ui.viewStack = append(ui.viewStack, view{viewType: viewTypeThread, context: dm.user.id})
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
			ui.showDialog(dialog.NewError(showError, ui.mainWindow), nil)
		}
	}
	userDetailsDialog = dialog.NewCustomConfirm(u.getName(), "Apply", "Cancel", editUserContainer, func(apply bool) { // TODO: add listener to update name when it changes
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
	}, ui.mainWindow)

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
		if ui.profile.id == id {
			return true
		}
	}

	return false
}

func (ui *ui) OpenNewGroupChat(bounceGroup chat.Group) { // TODO: rename "create and open"?
	ui.NewGroupChat(bounceGroup)

	group, exists := ui.groups[bounceGroup.ID]
	if exists {
		fyne.DoAndWait(func() {
			ui.showMainContainer()
			ui.displayThread(group)
		})
	} else {
		log.Error("cannot open newly created group because the UI isn't aware of it")
	}
}

func (ui *ui) NewGroupChat(bounceGroup chat.Group) {
	fyne.DoAndWait(func() { ui.buildNewGroupChat(bounceGroup) })
}

func (ui *ui) buildNewGroupChat(bounceGroup chat.Group) {
	if _, exists := ui.groups[bounceGroup.ID]; exists {
		return
	}

	group := &group{
		id:                             bounceGroup.ID,
		name:                           binding.NewString(),
		initial:                        binding.NewString(),
		images:                         bounceGroup.Images,
		users:                          newUserStore(),
		admins:                         bounceGroup.Admins,
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
		pendingUsersContainer:          container.NewVScroll(container.NewVBox()),
		adminChecks:                    make(map[uuid.UUID]*widget.Check),
		removeUserButtons:              make(map[uuid.UUID]*widget.Button),
		editUserDialogs:                make(map[uuid.UUID]dialogWithCallback),
		lastMessage:                    time.Now().Unix(),
	}

	for _, bu := range bounceGroup.Users {
		u, exists := ui.users.get(bu.ID)
		if !exists {
			u := makeUser(bu.ID, bu.Name)
			ui.users.add(u)
			group.users.add(u)
		} else {
			group.users.add(u)
		}
	}

	err := group.name.Set(bounceGroup.Name)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("data bindings are broken")
	}
	group.name.AddListener(binding.NewDataListener(func() {
		group.setInitial()
	}))

	group.editThreadNameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		group.editThreadNameEntry.Text = str
		if trimmed != 0 {
			group.editThreadNameEntry.CursorRow = 0
			group.editThreadNameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			group.editThreadNameEntry.Text = string(truncated)

		}
		group.editThreadNameEntry.Refresh()
	}

	group.notificationsEnabledCheck = widget.NewCheck("Enable notifications", func(_ bool) {})
	enabled := group.notificationsMutedUntil != chat.MutedForever
	group.notificationsEnabledCheck.SetChecked(enabled)

	group.restrictUserManagementCheck = widget.NewCheck("Restrict User Management", func(_ bool) {})
	group.restrictGroupEditsCheck = widget.NewCheck("Restrict Group Edits", func(_ bool) {})
	group.restrictPostingCheck = widget.NewCheck("Restrict Posting", func(_ bool) {})
	group.restrictUserManagementCheck.SetChecked(group.restrictUserManagement)
	group.restrictGroupEditsCheck.SetChecked(group.restrictGroupEdits)
	group.restrictPostingCheck.SetChecked(group.restrictPosting)

	ui.buildEditThreadContainer(group)
	editButton := widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() {
		ui.refreshUserSelections(group)
		ui.showEditThreadContainer(group)
	})
	editButton.Importance = widget.LowImportance

	group.headerIcon = newDefaultImage(group.id, bounceGroup.Images, group.initial, 32, ui.bounce.GetFileData, nil) // TODO: get size from theme
	groupLabelText := widget.NewLabel(bounceGroup.Name)

	var groupLabel *fyne.Container
	if fyne.CurrentDevice().IsMobile() {
		backButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
			ui.mobileBack()
		})
		backButton.Importance = widget.LowImportance
		groupLabel = container.NewHBox(
			backButton,
			group.headerIcon,
			groupLabelText,
		)
	} else {
		groupLabel = container.NewHBox(
			group.headerIcon,
			groupLabelText,
		)
	}

	groupLabelText.Bind(group.name)
	groupLabelText.TextStyle = fyne.TextStyle{Bold: true}
	group.header = container.New(
		layout.NewBorderLayout(nil, nil, groupLabel, editButton),
		groupLabel,
		editButton,
	)

	entry := newThreadEntry(5, func() bool { return len(group.pendingMessageAttachments.files) > 0 })
	group.entry = entry
	entry.OnChanged = func(_ string) {
		go ui.bounce.TypingInGroup(group.id)
	}
	entry.customOnSubmitted = func() {
		entry.Disable()
		gm := chat.GroupMessage{
			Thread: group.id,
			Text:   entry.Text,
		}

		imageAttachments := []chat.ImageAttachment{}
		fileAttachments := []chat.FileAttachment{}
		readers := map[uuid.UUID]io.ReadCloser{}
		sources := map[uuid.UUID]string{}

		pendingAttachments := group.pendingMessageAttachments.extract()
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
		ui.bounce.SendGroupMessage(gm, readers, sources)
		entry.Enable()
	}

	openThread := func() {
		ui.displayThread(group)
		ui.bounce.GroupConnectionDesired(group.id)
		if fyne.CurrentDevice().IsMobile() {
			ui.viewStack = append(ui.viewStack, view{viewType: viewTypeThread, context: group.id})
		}
	}
	group.button = newThreadButton(newDefaultImage(group.id, group.images, group.initial, 64, ui.bounce.GetFileData, openThread), group.name, openThread)
	group.scroll = ui.newChatHistory(group)

	// Keep the last message time counter up to date
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			fyne.Do(func() { group.button.updateLastMessageTimeText() })
		}
	}()

	group.typingIndicator = newTypingIndicator(typingIndicatorModeIcons, ui.bounce.GetFileData)
	group.typingIndicator.Hide()

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

			group.pendingMessageAttachments.add(reader)
		}, ui.mainWindow).Show() // We do not use showDialog here because on mobile this uses a native intent
	})
	addFiles.Importance = widget.LowImportance
	group.pendingMessageAttachments = newPendingMessageAttachments()
	footer := container.NewVBox(
		group.typingIndicator,
		group.pendingMessageAttachments,
		container.New(
			layout.NewBorderLayout(nil, nil, nil, addFiles),
			addFiles,
			group.entry,
		),
	)
	group.view = container.New(
		layout.NewBorderLayout(group.header, footer, nil, nil),
		group.header,
		footer,
		container.New(&autoscollLayout{}, group.scroll),
	)
	ui.groups[group.id] = group
	ui.refreshThreadOrder()
	ui.updateEnabledFeatures(group)

	ti, err := ui.newGroupCreated(group.id, group.id, bounceGroup.CreatedBy, bounceGroup.CreatedAt)
	if err != nil {
		log.WithFields(log.Fields{
			"error":      err.Error(),
			"group":      group.id,
			"created_by": bounceGroup.CreatedBy,
		}).Warn("error creating thread item for group creation")
	}
	ui.appendThreadItem(group, ti)
	ti.setButton(group.button)
}

func (ui *ui) SetGroupState(bounceGroup chat.Group) {
	fyne.DoAndWait(func() {
		g, exists := ui.groups[bounceGroup.ID]
		if !exists {
			log.WithFields(log.Fields{
				"group_id": bounceGroup.ID,
			}).Warn("cannot set state on unknown group")
			return
		}

		err := g.name.Set(bounceGroup.Name)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("data bindings are broken")
		}

		g.images = bounceGroup.Images
		g.editIcon.images = bounceGroup.Images
		g.editIcon.Refresh()
		g.headerIcon.images = bounceGroup.Images
		g.headerIcon.Refresh()
		g.button.threadImage.images = bounceGroup.Images
		g.button.threadImage.Refresh()

		g.users.empty()
		for _, bu := range bounceGroup.Users {
			u, ok := ui.users.get(bu.ID)
			if !ok {
				u = makeUser(bu.ID, bu.Name)
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
	})
}

func (ui *ui) DisplayGroupMessage(gm chat.GroupMessage) {
	g, exists := ui.groups[gm.Thread]
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
	g, exists := ui.groups[gm.Thread]
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

		g.entry.Text = ""
		g.entry.Refresh()

		g.chatHistoryScroll().ScrollToBottom()
		ui.chatContainer.Refresh()
	})
}

func (ui *ui) AddUser(ugau chat.UpdateGroupAddUser) {
	g, exists := ui.groups[ugau.Thread]
	if !exists {
		log.WithFields(log.Fields{
			"group_id": ugau.Thread,
		}).Error("group not found for update group add user")
		return
	}

	ti, err := ui.newUpdateGroupAddUser(ugau)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for adding user to group")
		return
	}

	u := makeUser(ugau.User.ID, ugau.User.Name)
	ui.users.add(u) // TODO: this is needed since it isn't in the group state, should it be?

	fyne.DoAndWait(func() { ui.appendThreadItem(g, ti) })
}

func (ui *ui) RemoveUser(ugru chat.UpdateGroupRemoveUser) {
	g, exists := ui.groups[ugru.Thread]
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
		if ui.activeThread == rfg.Group {
			if fyne.CurrentDevice().IsMobile() {
				ui.showMainContainer()
			} else {
				ui.chatContainer.Objects = []fyne.CanvasObject{ui.defaultContainer}
				ui.chatContainer.Refresh()
				ui.activeThread = uuid.Nil
			}
		}
		if rfg.Actor != ui.profile.id {
			var actorName string
			actor, ok := ui.users.get(rfg.Actor)
			if ok {
				actorName = actor.getName()
			} else {
				log.WithFields(log.Fields{
					"actor": rfg.Actor,
					"group": rfg.Group,
				}).Error("unknown user removed us from a group")
				actorName = "unknown user"
			}
			g, ok := ui.groups[rfg.Group]
			if !ok {
				log.WithFields(log.Fields{
					"group": rfg.Group,
				}).Error("removed from unknown group")
				return
			}
			groupName, err := g.name.Get()
			if err != nil {
				log.Fatal("data bindings are broken")
			}
			if !ui.initialSyncIncomplete {
				ui.showDialog(dialog.NewInformation("Removed From Group", actorName+" removed you from "+groupName, ui.mainWindow), nil)
			}
		}
		delete(ui.groups, rfg.Group)
		ui.refreshThreadOrder()
	})
}

func (ui *ui) GroupDeleted(gd chat.GroupDeleted) {
	fyne.DoAndWait(func() {
		if ui.activeThread == gd.Group {
			if fyne.CurrentDevice().IsMobile() {
				ui.showMainContainer()
			} else {
				ui.chatContainer.Objects = []fyne.CanvasObject{ui.defaultContainer}
				ui.chatContainer.Refresh()
				ui.activeThread = uuid.Nil
			}
		}

		if gd.Actor != ui.profile.id {
			var actorName string
			if gd.Actor == ui.profile.id {
				actorName = "You"
			} else {
				actor, ok := ui.users.get(gd.Actor)
				if ok {
					actorName = actor.getName()
				} else {
					log.WithFields(log.Fields{
						"actor": gd.Actor,
						"group": gd.Group,
					}).Warn("unknown user deleted group")
					actorName = "unknown user"
				}
			}

			g, ok := ui.groups[gd.Group]
			if !ok {
				log.WithFields(log.Fields{
					"group": gd.Group,
				}).Error("unknown group deleted")
				return
			}
			groupName, err := g.name.Get()
			if err != nil {
				log.Fatal("data bindings are broken")
			}
			if !ui.initialSyncIncomplete {
				ui.showDialog(dialog.NewInformation("Group Deleted", actorName+" deleted the group \""+groupName+"\"", ui.mainWindow), nil)
			}
		}

		delete(ui.groups, gd.Group)
		ui.refreshThreadOrder()
	})
}

func (ui *ui) RenameGroup(ugn chat.UpdateGroupName) {
	g, exists := ui.groups[ugn.Thread]
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
	g, exists := ui.groups[ugr.Thread]
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
	g, exists := ui.groups[ugch.Thread]
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
	g, exists := ui.groups[ugap.Thread]
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
	g, exists := ui.groups[ugad.Thread]
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
	g, exists := ui.groups[ugumr.Thread]
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
	g, exists := ui.groups[ugumu.Thread]
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
	g, exists := ui.groups[uger.Thread]
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
	g, exists := ui.groups[ugeu.Thread]
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
	g, exists := ui.groups[ugpr.Thread]
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
	g, exists := ui.groups[ugpu.Thread]
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
	g, exists := ui.groups[ubg.Thread]
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
	g, exists := ui.groups[ugci.Thread]
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

func (ui *ui) updateEnabledFeatures(g *group) {
	amAdmin := g.isAdmin(ui.profile.id)

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

func (ui *ui) PauseGroupNotifications(groupID uuid.UUID) {
	ui.pausedGroupNotificationsMutex.Lock()
	defer ui.pausedGroupNotificationsMutex.Unlock()

	ui.pausedGroupNotifications[groupID] = true
}

func (ui *ui) ResumeGroupNotifications(groupID uuid.UUID) {
	ui.pausedGroupNotificationsMutex.Lock()
	defer ui.pausedGroupNotificationsMutex.Unlock()

	delete(ui.pausedGroupNotifications, groupID)
}

func (ui *ui) groupNotificationsPaused(groupID uuid.UUID) bool {
	ui.pausedGroupNotificationsMutex.Lock()
	defer ui.pausedGroupNotificationsMutex.Unlock()

	_, ok := ui.pausedGroupNotifications[groupID]
	return ok
}
