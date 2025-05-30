package ui

import (
	"errors"
	"io"
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
	user                             *user
	notificationsMutedUntil          int64 // TODO: fyne feature request/PR: support binding int64 for time.Time
	retention                        int64
	overrideReadReceiptSetting       bool
	readReceiptsEnabled              bool
	overrideTypingIndicatorSetting   bool
	typingIndicatorsEnabled          bool
	editIcon                         *defaultImage
	headerIcon                       *defaultImage
	editContainer                    *fyne.Container
	view                             *fyne.Container
	header                           *fyne.Container
	button                           *threadButton
	typingIndicator                  *typingIndicator
	notificationsEnabledCheck        *widget.Check
	readReceiptOverrideSelection     *widget.Select
	typingIndicatorOverrideSelection *widget.Select
	scroll                           *chatHistory //List // TODO: rename to history
	entry                            *threadEntry
	retentionSelection               *widget.Select
	lastMessage                      int64
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

func (dm *directMessage) chatHistoryScroll() *chatHistory {
	return dm.scroll
}

func (dm *directMessage) getButton() *threadButton {
	return dm.button
}

func (dm *directMessage) getTypingIndicator() *typingIndicator {
	return dm.typingIndicator
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

func (dm *directMessage) refreshReadReceiptSettingSelection(options []string) {
	dm.readReceiptOverrideSelection.Options = options
	if !dm.overrideReadReceiptSetting {
		dm.readReceiptOverrideSelection.Selected = options[0]
	} else {
		if dm.readReceiptsEnabled {
			dm.readReceiptOverrideSelection.Selected = options[1]
		} else {
			dm.readReceiptOverrideSelection.Selected = options[2]
		}
	}
	dm.readReceiptOverrideSelection.Refresh()
}

func (dm *directMessage) refreshTypingIndicatorSettingSelection(options []string) {
	dm.typingIndicatorOverrideSelection.Options = options
	if !dm.overrideTypingIndicatorSetting {
		dm.typingIndicatorOverrideSelection.Selected = options[0]
	} else {
		if dm.typingIndicatorsEnabled {
			dm.typingIndicatorOverrideSelection.Selected = options[1]
		} else {
			dm.typingIndicatorOverrideSelection.Selected = options[2]
		}
	}
	dm.typingIndicatorOverrideSelection.Refresh()
}

func (fyneUI *Fyne) DMTypingIndicatorSettingsSet(userID uuid.UUID, override, enabled bool) {
	dm, exists := fyneUI.dms[userID]
	if !exists {
		log.WithFields(log.Fields{
			"userID": userID,
		}).Error("cannot set typing indicator override settings for unknown direct message")
		return
	}
	dm.overrideTypingIndicatorSetting = override
	dm.typingIndicatorsEnabled = enabled
}

func (fyneUI *Fyne) NewDirectMessage(bounceUser chat.User) {
	fyne.DoAndWait(func() { fyneUI.buildNewDirectMessage(bounceUser) })
}

func (fyneUI *Fyne) buildNewDirectMessage(bounceUser chat.User) {
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
		user:                           user,
		notificationsMutedUntil:        bounceUser.State.MutedUntil,
		retention:                      bounceUser.State.Retention,
		lastMessage:                    time.Now().Unix(),
		overrideReadReceiptSetting:     bounceUser.State.OverrideReadReceiptSetting,
		readReceiptsEnabled:            bounceUser.State.ReadReceiptsEnabled,
		overrideTypingIndicatorSetting: bounceUser.State.OverrideTypingIndicatorSetting,
		typingIndicatorsEnabled:        bounceUser.State.TypingIndicatorsEnabled,
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

	dm.headerIcon = newDefaultImage(user.id, bounceUser.Images, user.initials, 32, fyneUI.callbacks.GetFileData, nil) // TODO: get size from theme

	userLabelText := widget.NewLabelWithData(user.name)

	var userLabel *fyne.Container
	if fyne.CurrentDevice().IsMobile() {
		backButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
			fyneUI.mobileBack()
		})
		backButton.Importance = widget.LowImportance

		userLabel = container.NewHBox(
			backButton,
			dm.headerIcon,
			userLabelText,
		)
	} else {
		userLabel = container.NewHBox(
			dm.headerIcon,
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
	}

	openThread := func() {
		fyneUI.displayThread(dm)
		fyneUI.callbacks.UserConnectionDesired(user.id)
		if fyne.CurrentDevice().IsMobile() {
			fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeThread, context: dm.user.id})
		}
	}
	dm.button = newThreadButton(newDefaultImage(user.id, bounceUser.Images, user.initials, 64, fyneUI.callbacks.GetFileData, openThread), user.name, openThread)
	dm.button.Refresh()
	dm.scroll = newChatHistory(
		dm.user.id,
		fyneUI.profile.id,
		fyneUI.messages,
		fyneUI.callbacks.MarkAsRead,
		dm.button.setUnreadCount,
		func(id uuid.UUID) {
			fyneUI.callbacks.MarkAllDirectMessagesAsRead(id)
			fyneUI.mainWindow.Canvas().Focus(dm.entry)
		},
		func() bool { return fyneUI.focused },
		fyneUI.callbacks.GetFileData,
	)

	// Keep the last message time counter up to date
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			fyne.DoAndWait(func() { dm.button.updateLastMessageTimeText() })
		}
	}()

	dm.typingIndicator = newTypingIndicator(typingIndicatorModeIcons, fyneUI.callbacks.GetFileData)
	dm.typingIndicator.Hide()
	footer := container.NewVBox(
		dm.typingIndicator,
		dm.entry,
	)

	dm.view = container.New(
		layout.NewBorderLayout(dm.header, footer, nil, nil),
		dm.header,
		footer,
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

	fyne.DoAndWait(func() {
		fyneUI.appendThreadItem(dmThread, ti)
	})
}

func (fyneUI *Fyne) DisplaySentDirectMessage(dm chat.DirectMessage) {
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

	fyne.Do(func() {
		fyneUI.appendThreadItem(dmThread, ti)

		dmThread.entry.Text = ""
		dmThread.entry.Refresh()

		dmThread.chatHistoryScroll().ScrollToBottom()
		fyneUI.chatContainer.Refresh()
	})
}

func (fyneUI *Fyne) showEditDMContainer(dm *directMessage) {
	if fyne.CurrentDevice().IsMobile() {
		fyneUI.viewStack = append(fyneUI.viewStack, view{viewType: viewTypeDMSettings, context: dm.user.id})
	}
	fyneUI.mainWindow.SetContent(dm.editContainer)
	dm.editContainer.Show()
}

func (fyneUI *Fyne) buildEditDMContainer(dm *directMessage) {
	var selectImage func()
	if dm.user.id == fyneUI.profile.id {
		selectImage = func() {
			dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
				if reader == nil {
					return
				}

				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Debug("error selecting new profile image")
					return
				}

				data, err := io.ReadAll(reader)
				reader.Close()
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Debug("error reading new profile image")
					return
				}

				// TODO: make sure data is a valid image, allow for editing, etc
				// TODO: don't actualy set it until they hit save?

				fyneUI.callbacks.UpdateProfileImage(data)
			}, fyneUI.mainWindow).Show() // We do not use showDialog here because on mobile this uses a native intent
		}
	}
	dm.editIcon = newDefaultImage(dm.user.id, dm.user.images, dm.user.initials, 128, fyneUI.callbacks.GetFileData, selectImage)

	username := widget.NewLabelWithData(dm.user.name)

	//
	// Selection for message retention
	//
	dm.retentionSelection = widget.NewSelect(retentionSelections, nil)
	dm.retentionSelection.Selected = getRetentionName(dm.retention)

	// Overrides for read receipts and typing indicators
	dm.readReceiptOverrideSelection = widget.NewSelect(fyneUI.readReceiptOverrideSelectionOptions(), nil)
	dm.refreshReadReceiptSettingSelection(fyneUI.readReceiptOverrideSelectionOptions())
	dm.typingIndicatorOverrideSelection = widget.NewSelect(fyneUI.typingIndicatorOverrideSelectionOptions(), nil)
	dm.refreshTypingIndicatorSettingSelection(fyneUI.typingIndicatorOverrideSelectionOptions())

	//
	// Button to clear all message history, with confirm dialog
	//
	returnToThread := false
	var showError error
	confirmClearHistory := dialog.NewConfirm(
		"Clear all message history?",
		"Are you sure you want to permanently delete all chat history on all devices?",
		func(confirmed bool) {
			returnToThread = confirmed
			showError = nil
			if confirmed {
				err := fyneUI.callbacks.ClearDMChatHistory(dm.user.id)
				if err != nil {
					showError = errors.New("error clearing chat history: " + err.Error())
					returnToThread = false
				}
			}
		},
		fyneUI.mainWindow,
	)
	dialogCleanup := func() {
		if returnToThread {
			if fyne.CurrentDevice().IsMobile() {
				fyneUI.mobileBack()
			} else {
				fyneUI.showMainContainer()
			}
			fyneUI.mainWindow.Canvas().Focus(dm.getEntry())
		}
		if showError != nil {
			fyneUI.showDialog(dialog.NewError(showError, fyneUI.mainWindow), nil)
		}
	}
	clearHistoryButton := widget.NewButton("Clear history", func() {
		fyneUI.showDialog(confirmClearHistory, dialogCleanup)
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
			fyneUI.showDialog(dialog.NewError(errors.New("invalid retention selection: "+selectedRetentionString), fyneUI.mainWindow), nil)
			return
		} else {
			if dm.retention != selectedRetentionValue {
				fyneUI.callbacks.SetDMRetention(dm.user.id, selectedRetentionValue) // TODO: this should return an error if the user ID is nonsense
			}
		}

		// Capture read receipt selection state
		readReceiptSelection := dm.readReceiptOverrideSelection.Selected
		defaultReadReceiptSelected := readReceiptSelection == defaultOn || readReceiptSelection == defaultOff
		switchedReadReceiptDefault := (defaultReadReceiptSelected && dm.overrideReadReceiptSetting) || (!defaultReadReceiptSelected && !dm.overrideReadReceiptSetting)
		switchedReadReceiptEnabled := (readReceiptSelection == off && dm.readReceiptsEnabled) || (readReceiptSelection == on && !dm.readReceiptsEnabled)
		// Capture typing indicator selection state
		typingIndicatorSelection := dm.typingIndicatorOverrideSelection.Selected
		defaultTypingIndicatorSelected := typingIndicatorSelection == defaultOn || typingIndicatorSelection == defaultOff
		switchedTypingIndicatorDefault := (defaultTypingIndicatorSelected && dm.overrideTypingIndicatorSetting) || (!defaultTypingIndicatorSelected && !dm.overrideTypingIndicatorSetting)
		switchedTypingIndicatorEnabled := (typingIndicatorSelection == off && dm.typingIndicatorsEnabled) || (typingIndicatorSelection == on && !dm.typingIndicatorsEnabled)
		// Set values if needed
		if switchedReadReceiptDefault || switchedReadReceiptEnabled {
			if defaultReadReceiptSelected {
				fyneUI.callbacks.SetDMReadReceiptSettings(dm.user.id, false, true) // TODO: error check all of these
			} else if readReceiptSelection == on {
				fyneUI.callbacks.SetDMReadReceiptSettings(dm.user.id, true, true)
			} else if readReceiptSelection == off {
				fyneUI.callbacks.SetDMReadReceiptSettings(dm.user.id, true, false)
			} else {
				log.WithFields(log.Fields{
					"user_id":   dm.user.id,
					"selection": readReceiptSelection,
				}).Error("invalid selection for read receipt override")
			}
		}
		if switchedTypingIndicatorDefault || switchedTypingIndicatorEnabled {
			if defaultTypingIndicatorSelected {
				fyneUI.callbacks.SetDMTypingIndicatorSettings(dm.user.id, false, true)
			} else if typingIndicatorSelection == on {
				fyneUI.callbacks.SetDMTypingIndicatorSettings(dm.user.id, true, true)
			} else if typingIndicatorSelection == off {
				fyneUI.callbacks.SetDMTypingIndicatorSettings(dm.user.id, true, false)
			} else {
				log.WithFields(log.Fields{
					"user_id":   dm.user.id,
					"selection": typingIndicatorSelection,
				}).Error("invalid selection for typing indicator override")
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

		// Reset the read receipt and typing indicator overrides
		dm.refreshReadReceiptSettingSelection(fyneUI.readReceiptOverrideSelectionOptions())
		dm.refreshTypingIndicatorSettingSelection(fyneUI.typingIndicatorOverrideSelectionOptions())

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

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), cancelChanges)
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	editDMFeatures := container.NewVBox(
		container.NewCenter(dm.editIcon),
		container.NewCenter(username),
		dm.notificationsEnabledCheck,
		widget.NewLabel("Disappearing Messages"),
		dm.retentionSelection,
		container.NewHBox(clearHistoryButton),
		widget.NewAccordion(
			&widget.AccordionItem{
				Title: "Advanced Options",
				Detail: container.NewVBox(
					widget.NewLabel("Read Receipts"),
					dm.readReceiptOverrideSelection,
					widget.NewLabel("Typing Indicators"),
					dm.typingIndicatorOverrideSelection,
				),
			},
		),
	)
	dm.editContainer = container.NewMax(
		container.New(
			layout.NewBorderLayout(closeBar, actionButtons, nil, nil),
			container.NewVScroll(editDMFeatures),
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

	fyne.DoAndWait(func() {
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

		// Update read receipt and typign indicator selections
		dm.overrideReadReceiptSetting = state.OverrideReadReceiptSetting
		dm.readReceiptsEnabled = state.ReadReceiptsEnabled
		dm.refreshReadReceiptSettingSelection(fyneUI.readReceiptOverrideSelectionOptions())
		dm.overrideTypingIndicatorSetting = state.OverrideTypingIndicatorSetting
		dm.typingIndicatorsEnabled = state.TypingIndicatorsEnabled
		dm.refreshTypingIndicatorSettingSelection(fyneUI.typingIndicatorOverrideSelectionOptions())
	})
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

	fyne.DoAndWait(func() { fyneUI.appendThreadItem(dmThread, ti) })
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

	fyne.DoAndWait(func() { fyneUI.appendThreadItem(dmThread, ti) })
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

		fyne.DoAndWait(func() {
			fyneUI.NewDirectMessage(chat.User{
				ID:   u.id,
				Name: u.getName(),
			})
		})
		dm, dmExists = fyneUI.dms[id]
		if !dmExists {
			return dm, errors.New("direct message not found after creation")
		}
	}

	return dm, nil
}
