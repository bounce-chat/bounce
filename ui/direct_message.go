package ui

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

type directMessage struct {
	user                      *user
	notificationsMutedUntil   int64 // TODO: fyne feature request/PR: support binding int64 for time.Time
	retention                 int64
	editContainer             *fyne.Container
	view                      *fyne.Container
	header                    *fyne.Container
	button                    *threadButton
	notificationsEnabledCheck *widget.Check
	scroll                    *container.Scroll
	entry                     *threadEntry
	retentionSelection        *widget.Select
	lastMessage               int64
}

func (dm *directMessage) getID() uuid.UUID {
	return dm.user.id
}

func (dm *directMessage) getView() *fyne.Container {
	return dm.view
}
func (dm *directMessage) getEntry() *threadEntry {
	return dm.entry
}

func (dm *directMessage) chatHistoryScroll() *container.Scroll {
	return dm.scroll
}

func (dm *directMessage) getButton() *threadButton {
	return dm.button
}

func (dm *directMessage) getLastMessageTime() int64 {
	return dm.lastMessage
}

func (dm *directMessage) setLastMessageTime(time int64) {
	dm.lastMessage = time
}

func (dm *directMessage) getNotificationsMutedUntil() int64 {
	return dm.notificationsMutedUntil
}

func (fyneUI *Fyne) NewDirectMessage(bounceUser chat.User) {
	user, exists := fyneUI.users.get(bounceUser.ID)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": bounceUser.ID,
		}).Error("cannot create DM with user unknown to the UI")
		return
	}
	if _, exists := fyneUI.dms[bounceUser.ID]; exists {
		log.WithFields(log.Fields{
			"user_id": bounceUser.ID,
		}).Error("attempt to create a DM that already exists, ignoring")
		return
	}

	dm := &directMessage{
		user:                    user,
		notificationsMutedUntil: bounceUser.State.MutedUntil,
		retention:               bounceUser.State.Retention,
		scroll:                  container.NewVScroll(container.NewVBox()),
		lastMessage:             time.Now().Unix(),
	}

	dm.notificationsEnabledCheck = widget.NewCheck("Enable notifications", func(_ bool) {})
	var err error
	dm.notificationsMutedUntil = bounceUser.State.MutedUntil
	if err != nil {
		log.WithFields(log.Fields{
			"user_id": dm.user.id,
		}).Error("cannot find muted until for user")
		return
		// TODO: fatal?
	}
	enabled := dm.notificationsMutedUntil != chat.MutedForever
	dm.notificationsEnabledCheck.SetChecked(enabled)

	fyneUI.buildEditDMContainer(dm)
	editButton := widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() {
		fyneUI.showEditDMContainer(dm)
	})
	editButton.Importance = widget.LowImportance

	userIconCanvas := newDefaultImage(user.id, user.initials, 32, nil) // TODO: get size from theme

	userLabelText := widget.NewLabelWithData(user.name)

	var userLabel *fyne.Container
	if fyne.CurrentDevice().IsMobile() {
		backButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
			fyneUI.mobileBack()
		})
		backButton.Importance = widget.LowImportance

		userLabel = container.NewHBox(
			backButton,
			userIconCanvas,
			userLabelText,
		)
	} else {
		userLabel = container.NewHBox(
			userIconCanvas,
			userLabelText,
		)
	}

	//userLabelText.Bind(user.name) // TODO: user objects should have bindings as names and take updates from the chat engine
	userLabelText.TextStyle = fyne.TextStyle{Bold: true}
	dm.header = container.New(
		layout.NewBorderLayout(nil, nil, userLabel, editButton),
		userLabel,
		editButton,
	)

	//
	// BUild the entry bar
	//
	entry := newThreadEntry(5)
	dm.entry = entry
	entry.OnChanged = func(_ string) {
		go fyneUI.callbacks.TypingInDirectMessage(dm.user.id)
	}
	entry.customOnSubmitted = func() {
		fyneUI.callbacks.SendDirectMessage(chat.DirectMessage{
			Thread: dm.user.id,
			Text:   entry.Text,
		})

		entry.Text = ""
		entry.Refresh()

		dm.chatHistoryScroll().ScrollToBottom()
		dm.chatHistoryScroll().Refresh()
	}

	openThread := func() {
		fyneUI.displayThread(dm)
		fyneUI.callbacks.UserConnectionDesired(user.id)
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeThread, context: dm.user.id})
		}
	}
	dm.button = newThreadButton(newDefaultImage(user.id, user.initials, 64, openThread), user.name, openThread)

	// Keep the last message time counter up to date
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			dm.button.updateLastMessageTimeText()
		}
	}()

	dm.view = container.New(
		layout.NewBorderLayout(dm.header, dm.entry, nil, nil),
		dm.header,
		dm.entry,
		container.New(&autoscollLayout{}, dm.scroll),
	)
	fyneUI.dms[bounceUser.ID] = dm
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) DisplayDirectMessage(dm chat.DirectMessage) {
	dmThread, err := fyneUI.getOrCreateDM(dm.Thread)
	if err != nil {
		log.Fatal("DM doesn't exist immediately after creation")
	}

	ti, err := fyneUI.newDirectMessage(dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for direct message")
		return
	}

	if !fyneUI.isActive(dmThread) && !(dm.Author == fyneUI.profile.id) {
		dmThread.button.addUnread()
	}

	fyneUI.appendThreadItem(dmThread, ti)
}

func (fyneUI *Fyne) showEditDMContainer(dm *directMessage) {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeDMSettings, context: dm.user.id})
	}
	fyneUI.mainWindow.SetContent(dm.editContainer)
	dm.editContainer.Show()
}

func (fyneUI *Fyne) buildEditDMContainer(dm *directMessage) {
	threadIcon := newDefaultImage(dm.user.id, dm.user.initials, 128, nil)

	username := widget.NewLabelWithData(dm.user.name)

	//
	// Selection for message retention
	//
	dm.retentionSelection = widget.NewSelect(retentionSelections, nil)
	dm.retentionSelection.Selected = getRetentionName(dm.retention)

	//
	// Button to clear all message history, with confirm dialog
	//
	confirmClearHistory := dialog.NewConfirm(
		"Clear all message history?",
		"Are you sure you want to permanently delete all chat history on all devices?",
		func(confirmed bool) {
			if confirmed {
				fyneUI.callbacks.ClearDMChatHistory(dm.user.id)
				fyneUI.showMainContainer() // TODO: mobile
				fyneUI.mainWindow.Canvas().Focus(dm.getEntry())
			}
		},
		fyneUI.mainWindow,
	)
	clearHistoryButton := widget.NewButton("Clear history", func() {
		confirmClearHistory.Show()
	})

	//
	// Save and Cancel buttons
	//

	saveButton := widget.NewButton("Save", func() {
		// Update notification muted until if the selection doesn't line up with what we have for this user
		notificationsEnabled := dm.notificationsMutedUntil != chat.MutedForever
		if dm.notificationsEnabledCheck.Checked != notificationsEnabled {
			mutedUntil := int64(0)
			if !dm.notificationsEnabledCheck.Checked {
				mutedUntil = chat.MutedForever
			}
			fyneUI.callbacks.SetDMMutedUntil(dm.user.id, mutedUntil)
		}
		// Update message retention if the selection doesn't line up with what we have for this user
		selectedRetentionString := dm.retentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			dialog.ShowError(errors.New("invalid retention selection: "+selectedRetentionString), fyneUI.mainWindow)
		} else {
			if dm.retention != selectedRetentionValue {
				fyneUI.callbacks.SetDMRetention(dm.user.id, selectedRetentionValue) // TODO: this should return an error if the user ID is nonsense
			}
		}
		// Go back to the thread after settings updates are done
		fyneUI.showMainContainer()
		fyneUI.mainWindow.Canvas().Focus(dm.getEntry())
	})
	saveButton.Importance = widget.HighImportance

	cancelChanges := func() {
		// Reset retention
		dm.retentionSelection.Selected = getRetentionName(dm.retention)
		dm.retentionSelection.Refresh()

		// Reset notification settings
		enabled := dm.notificationsMutedUntil != chat.MutedForever
		dm.notificationsEnabledCheck.SetChecked(enabled)

		// Show main tontainer
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.mobileBack()
		} else {
			fyneUI.showMainContainer()
			fyneUI.mainWindow.Canvas().Focus(dm.getEntry())
		}
	}
	cancelButton := widget.NewButton("Cancel", cancelChanges)

	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, saveButton),
		saveButton,
		cancelButton,
	)
	topOptionsVBox := container.NewVBox(
		container.NewCenter(threadIcon),
		container.NewCenter(username),
		dm.notificationsEnabledCheck,
		widget.NewLabel("Disappearing Messages"),
		dm.retentionSelection,
		container.NewHBox(clearHistoryButton),
	)

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), cancelChanges)
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	// TODO: add option to introduce this contact to someone
	dm.editContainer = container.NewMax(
		container.New(
			layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
			container.New(
				layout.NewBorderLayout(topOptionsVBox, nil, nil, nil),
				topOptionsVBox,
			),
			closeBar,
			actionButtons,
		),
	)

}

func (fyneUI *Fyne) SetDMState(userID uuid.UUID, state chat.DMState) {
	// Find the thread
	dm, exists := fyneUI.dms[userID]
	if !exists {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Error("cannot set state of unknown dm")
		return
	}

	// Update retention
	dm.retention = state.Retention
	newRetentionName := getRetentionName(dm.retention)
	dm.retentionSelection.Selected = newRetentionName
	dm.retentionSelection.Refresh()

	// Update muted until
	dm.notificationsMutedUntil = state.MutedUntil
	enabled := dm.notificationsMutedUntil != chat.MutedForever
	dm.notificationsEnabledCheck.SetChecked(enabled)
	dm.notificationsEnabledCheck.Refresh()
}

func (fyneUI *Fyne) DMChatHistoryCleared(udch chat.UpdateDMClearHistory) {
	dmThread, err := fyneUI.getOrCreateDM(udch.Thread)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"user_id": udch.Thread,
		}).Error("cannot clear history for unknown dm thread")
		return
	}

	ti, err := fyneUI.newUpdateDMClearHistory(udch)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for clearing dm history")
		return
	}

	fyneUI.appendThreadItem(dmThread, ti)
}

func (fyneUI *Fyne) DMRetentionChanged(udr chat.UpdateDMRetention) {
	dmThread, err := fyneUI.getOrCreateDM(udr.Thread)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"user_id": udr.Thread,
		}).Error("cannot update retention for unknown dm thread")
		return
	}

	ti, err := fyneUI.newUpdateDMRetention(udr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update dm retention")
		return
	}

	fyneUI.appendThreadItem(dmThread, ti)
}

func (fyneUI *Fyne) getOrCreateDM(id uuid.UUID) (*directMessage, error) {
	var dm *directMessage

	var dmExists bool
	dm, dmExists = fyneUI.dms[id]
	if !dmExists {
		u, userExists := fyneUI.users.get(id)
		if !userExists {
			return dm, errUnknownUser
		}

		fyneUI.NewDirectMessage(chat.User{
			ID:   u.id,
			Name: u.getName(),
		})
		dm, dmExists = fyneUI.dms[id]
		if !dmExists {
			return dm, errors.New("direct message not found after creation")
		}
	}

	return dm, nil
}
