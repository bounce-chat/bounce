package ui

import (
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
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
	editContainer             *fyne.Container
	view                      *fyne.Container
	header                    *fyne.Container
	button                    *threadButton
	notificationsEnabledCheck *widget.Check
	scroll                    *container.Scroll
	entry                     *threadEntry
	entryBar                  *fyne.Container
	retentionSelection        *widget.Select
	lastMessage               int64
	items                     threadItems
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

func (fyneUI *Fyne) populateUserItems(u *directMessage) { // TODO: make this work on threads generically?
	sort.Sort(u.items)

	chatHistory := u.chatHistoryScroll().Content.(*fyne.Container)
	for _, item := range u.items {
		chatHistory.Objects = append(chatHistory.Objects, item.widget) // TODO: g.appendItem()?
	}

	lastItem := u.items[len(u.items)-1]
	if lastItem.setButton != nil {
		lastItem.setButton(u.getButton())
	} else {
		log.Warn("thread item doesn't support setting last button")
	}
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) NewDirectMessage(bounceUser chat.User) { // TODO: Should wrap around something that takes the internal user object
	user, exists := fyneUI.users.get(bounceUser.ID)
	if !exists {
		log.WithFields(log.Fields{
			"user_id":   bounceUser.ID,
			"user_name": bounceUser.Name,
		}).Error("cannot create DM with user unknown to the UI")
		return
	}
	if _, exists := fyneUI.dms[bounceUser.ID]; exists {
		log.WithFields(log.Fields{
			"user_id":   bounceUser.ID,
			"user_name": bounceUser.Name,
		}).Error("attempt to create a DM that already exists, ignoring")
		return
	}

	dm := &directMessage{
		user:        user,
		scroll:      container.NewVScroll(container.NewVBox()),
		lastMessage: time.Now().Unix(),
	}

	dm.notificationsEnabledCheck = widget.NewCheck("Enable notifications", func(_ bool) {})
	var err error
	dm.notificationsMutedUntil, err = fyneUI.callbacks.GetDMMutedUntil(dm.user.id)
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
	editButton := widget.NewButton("Edit", func() {
		fyneUI.showEditDMContainer(dm)
	})
	userIconCanvas := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	userIconCanvas.FillMode = canvas.ImageFillContain
	userIconCanvas.SetMinSize(fyne.NewSize(32, 32))

	userLabelText := widget.NewLabel(user.name)
	userLabel := container.NewHBox(
		userIconCanvas,
		userLabelText,
	)

	//userLabelText.Bind(user.name) // TODO: user objects should have bindings as names and take updates from the chat engine
	userLabelText.TextStyle = fyne.TextStyle{Bold: true}
	dmButtons := container.NewMax(editButton)
	dm.header = container.New(
		layout.NewBorderLayout(nil, nil, userLabel, dmButtons),
		userLabel,
		dmButtons,
	)

	//
	// BUild the entry bar
	//
	entry := newThreadEntry(5)
	dm.entry = entry
	entry.OnChanged = func(_ string) {
		fyneUI.callbacks.TypingInDirectMessage(dm.user.id)
	}
	entry.customOnSubmitted = func() {
		message := &chat.DirectMessage{
			Thread: dm.user.id,
			Text:   entry.Text,
		}

		fyneUI.callbacks.SendDirectMessage(message)
		fyneUI.displaySentMessage(dm, message.ID, message.Text) // TODO: just build the bubble around the actual object?

		entry.Text = ""
		entry.Refresh()
	}
	dm.entryBar = container.NewMax(entry)

	dm.button = newThreadButton(todoImage(), user.name, func() {
		fyneUI.displayThread(dm)
		fyneUI.callbacks.UserConnectionDesired(user.id)
	})
	// Keep the last message time counter up to date
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			dm.button.updateLastMessageTimeText()
		}
	}()

	dm.view = container.New(
		layout.NewBorderLayout(dm.header, dm.entryBar, nil, nil),
		dm.header,
		dm.entryBar,
		dm.scroll,
	)
	fyneUI.dms[bounceUser.ID] = dm
	fyneUI.refreshThreadOrder()
}

func (fyneUI *Fyne) ReceivedDirectMessage(dm chat.DirectMessage) {
	u, exists := fyneUI.users.get(dm.Thread)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": dm.Thread,
		}).Error("chat engine sent DM from user unknown to UI")
		return
	}

	dmThread, exists := fyneUI.dms[dm.Thread]
	if !exists {
		fyneUI.NewDirectMessage(chat.User{
			ID:   dm.Thread,
			Name: u.name,
		})
		dmThread, exists = fyneUI.dms[dm.Thread]
		if !exists {
			log.Fatal("DM doesn't exist immediately after creation")
		}
	}

	ti, err := fyneUI.newDirectMessage(dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for direct message")
		return
	}

	fyneUI.threadWithMessageMutex.Lock()
	fyneUI.threadWithMessage[dm.ID] = dmThread
	fyneUI.threadWithMessageMutex.Unlock()

	if !fyneUI.isActive(dmThread) && !(dm.Author == fyneUI.profile.id) {
		dmThread.button.addUnread()
	}

	fyneUI.appendThreadItem(dmThread, ti)
}

func (fyneUI *Fyne) showEditDMContainer(dm *directMessage) {
	fyneUI.mainWindow.SetContent(dm.editContainer)
	dm.editContainer.Show()
}

func (fyneUI *Fyne) buildEditDMContainer(dm *directMessage) {
	threadIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png"))
	threadIcon.FillMode = canvas.ImageFillContain
	threadIcon.SetMinSize(fyne.NewSize(64, 64))

	username := widget.NewLabel(dm.user.name)

	//
	// Selection for message retention
	//
	dm.retentionSelection = widget.NewSelect(retentionSelections, nil)
	retention := fyneUI.callbacks.GetDMRetention(dm.user.id)
	dm.retentionSelection.Selected = getRetentionName(retention)

	//
	// Button to clear all message history, with confirm dialog
	//
	confirmClearHistory := dialog.NewConfirm(
		"Clear all message history?",
		"Are you sure you want to permanently delete all chat history on all devices?",
		func(confirmed bool) {
			if confirmed {
				fyneUI.callbacks.ClearDMChatHistory(dm.user.id)
				fyneUI.showMainContainer()
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
		currentRetention := fyneUI.callbacks.GetDMRetention(dm.user.id)
		selectedRetentionString := dm.retentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			dialog.ShowError(errors.New("invalid retention selection: "+selectedRetentionString), fyneUI.mainWindow)
		} else {
			if currentRetention != selectedRetentionValue {
				fyneUI.callbacks.SetDMRetention(dm.user.id, selectedRetentionValue) // TODO: this should return an error if the user ID is nonsense
			}
		}
		// Go back to the thread after settings updates are done
		fyneUI.showMainContainer()
		fyneUI.mainWindow.Canvas().Focus(dm.getEntry())
	})
	saveButton.Importance = widget.HighImportance
	cancelButton := widget.NewButton("Cancel", func() {
		fyneUI.showMainContainer()
		fyneUI.mainWindow.Canvas().Focus(dm.getEntry())
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, cancelButton, saveButton),
		saveButton,
		cancelButton,
	)
	topOptionsVBox := container.NewVBox(
		threadIcon,
		username,
		dm.notificationsEnabledCheck,
		widget.NewLabel("Disappearing Messages"),
		dm.retentionSelection,
		container.NewHBox(clearHistoryButton),
	)

	// Close the window but save state.  TODO: should it clear state as well?
	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		fyneUI.showMainContainer()
		fyneUI.mainWindow.Canvas().Focus(dm.getEntry())
	})
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

func (fyneUI *Fyne) DMChatHistoryCleared(udch chat.UpdateDMClearHistory) {
	dmThread, exists := fyneUI.dms[udch.Thread]
	if !exists {
		log.WithFields(log.Fields{
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

	// TODO: insertion sort this into the proper place in the thread?
	fyneUI.appendThreadItem(dmThread, ti)
}

func (fyneUI *Fyne) DMMutedUntilChanged(userID uuid.UUID, mutedUntil int64) {
	if dm, exists := fyneUI.dms[userID]; exists {
		dm.notificationsMutedUntil = mutedUntil
		enabled := mutedUntil != chat.MutedForever
		dm.notificationsEnabledCheck.SetChecked(enabled)
	} else {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Warn("cannot notify messages cleared for user that doesn't exist")
	}
}

func (fyneUI *Fyne) DMRetentionChanged(udr chat.UpdateDMRetention) {
	dmThread, exists := fyneUI.dms[udr.Thread]
	if !exists {
		log.WithFields(log.Fields{
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

	newRetentionName := getRetentionName(udr.Retention)
	dmThread.retentionSelection.Selected = newRetentionName
	dmThread.retentionSelection.Refresh()

	// TODO: insertion sort this into the proper place in the thread?
	fyneUI.appendThreadItem(dmThread, ti)
}
