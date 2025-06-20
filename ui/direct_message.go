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
	pendingMessageAttachments        *pendingMessageAttachments
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

func (ui *ui) DMTypingIndicatorSettingsSet(userID uuid.UUID, override, enabled bool) {
	dm, exists := ui.dms[userID]
	if !exists {
		log.WithFields(log.Fields{
			"userID": userID,
		}).Error("cannot set typing indicator override settings for unknown direct message")
		return
	}
	dm.overrideTypingIndicatorSetting = override
	dm.typingIndicatorsEnabled = enabled
}

func (ui *ui) NewDirectMessage(bounceUser chat.User) {
	fyne.DoAndWait(func() { ui.buildNewDirectMessage(bounceUser) })
}

func (ui *ui) buildNewDirectMessage(bounceUser chat.User) {
	user, exists := ui.users.get(bounceUser.ID)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": bounceUser.ID,
		}).Error("cannot create DM with user unknown to the UI")
		return
	}
	if _, exists := ui.dms[bounceUser.ID]; exists {
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

	ui.buildEditDMContainer(dm)
	editButton := widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() {
		ui.showEditDMContainer(dm)
	})
	editButton.Importance = widget.LowImportance

	dm.headerIcon = newDefaultImage(user.id, bounceUser.Images, user.initials, 32, ui.bounce.GetFileData, nil) // TODO: get size from theme

	userLabelText := widget.NewLabelWithData(user.name)

	var userLabel *fyne.Container
	if fyne.CurrentDevice().IsMobile() {
		backButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
			ui.mobileBack()
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
	entry := newThreadEntry(5, func() bool { return len(dm.pendingMessageAttachments.files) > 0 })
	dm.entry = entry
	entry.OnChanged = func(_ string) {
		go ui.bounce.TypingInDirectMessage(dm.user.id)
	}
	entry.customOnSubmitted = func() {
		chatDM := chat.DirectMessage{
			Thread: dm.user.id,
			Text:   entry.Text,
		}

		imageAttachments := []chat.ImageAttachment{}
		fileAttachments := []chat.FileAttachment{}
		readers := map[uuid.UUID]io.ReadCloser{}

		pendingAttachments := dm.pendingMessageAttachments.extract()
		for _, pma := range pendingAttachments {
			readers[pma.id] = pma.reader

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

		chatDM.ImageAttachments = imageAttachments
		chatDM.FileAttachments = fileAttachments

		ui.bounce.SendDirectMessage(chatDM, readers)
	}

	openThread := func() {
		ui.displayThread(dm)
		ui.bounce.UserConnectionDesired(user.id)
		if fyne.CurrentDevice().IsMobile() {
			ui.viewStack = append(ui.viewStack, view{viewType: viewTypeThread, context: dm.user.id})
		}
	}
	dm.button = newThreadButton(newDefaultImage(user.id, bounceUser.Images, user.initials, 64, ui.bounce.GetFileData, openThread), user.name, openThread)
	dm.button.Refresh()
	dm.scroll = ui.newChatHistory(dm)

	// Keep the last message time counter up to date
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			fyne.Do(func() { dm.button.updateLastMessageTimeText() })
		}
	}()

	dm.typingIndicator = newTypingIndicator(typingIndicatorModeIcons, ui.bounce.GetFileData)
	dm.typingIndicator.Hide()

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

			dm.pendingMessageAttachments.add(reader)
		}, ui.mainWindow).Show() // We do not use showDialog here because on mobile this uses a native intent
	})
	addFiles.Importance = widget.LowImportance
	dm.pendingMessageAttachments = newPendingMessageAttachments()

	footer := container.NewVBox(
		dm.typingIndicator,
		dm.pendingMessageAttachments,
		container.New(
			layout.NewBorderLayout(nil, nil, nil, addFiles),
			addFiles,
			dm.entry,
		),
	)

	dm.view = container.New(
		layout.NewBorderLayout(dm.header, footer, nil, nil),
		dm.header,
		footer,
		container.New(&autoscollLayout{}, dm.scroll),
	)
	ui.dms[bounceUser.ID] = dm
	ui.refreshThreadOrder()
}

func (ui *ui) DisplayDirectMessage(dm chat.DirectMessage) {
	dmThread, err := ui.getOrCreateDM(dm.Thread)
	if err != nil {
		log.Fatal("DM doesn't exist immediately after creation")
	}

	ti, err := ui.newDirectMessage(dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for direct message")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(dmThread, ti) })
}

func (ui *ui) DisplaySentDirectMessage(dm chat.DirectMessage) {
	dmThread, err := ui.getOrCreateDM(dm.Thread)
	if err != nil {
		log.Fatal("DM doesn't exist immediately after creation")
	}

	ti, err := ui.newDirectMessage(dm)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for direct message")
		return
	}

	fyne.DoAndWait(func() {
		ui.appendThreadItem(dmThread, ti)

		dmThread.entry.Text = ""
		dmThread.entry.Refresh()

		dmThread.chatHistoryScroll().ScrollToBottom()
		ui.chatContainer.Refresh()
	})
}

func (ui *ui) showEditDMContainer(dm *directMessage) {
	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeDMSettings, context: dm.user.id})
	}
	ui.mainWindow.SetContent(dm.editContainer)
	dm.editContainer.Show()
}

func (ui *ui) buildEditDMContainer(dm *directMessage) {
	var selectImage func()
	if dm.user.id == ui.profile.id {
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

				var size int64
				size, reader, err = fileSizeInReader(reader)
				if err != nil {
					log.WithFields(log.Fields{
						"error": err.Error(),
					}).Error("error getting file size")
					reader.Close()
					return
				}
				if size > chat.EmbeddedFileLimit {
					ui.showDialog(dialog.NewError(errors.New("file is too large"), ui.mainWindow), nil)
					reader.Close()
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

				ui.bounce.UpdateProfileImage(data)
			}, ui.mainWindow).Show() // We do not use showDialog here because on mobile this uses a native intent
		}
	}
	dm.editIcon = newDefaultImage(dm.user.id, dm.user.images, dm.user.initials, 128, ui.bounce.GetFileData, selectImage)

	username := widget.NewLabelWithData(dm.user.name)

	//
	// Selection for message retention
	//
	dm.retentionSelection = widget.NewSelect(retentionSelections, nil)
	dm.retentionSelection.Selected = getRetentionName(dm.retention)

	// Overrides for read receipts and typing indicators
	dm.readReceiptOverrideSelection = widget.NewSelect(ui.readReceiptOverrideSelectionOptions(), nil)
	dm.refreshReadReceiptSettingSelection(ui.readReceiptOverrideSelectionOptions())
	dm.typingIndicatorOverrideSelection = widget.NewSelect(ui.typingIndicatorOverrideSelectionOptions(), nil)
	dm.refreshTypingIndicatorSettingSelection(ui.typingIndicatorOverrideSelectionOptions())

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
				err := ui.bounce.ClearDMChatHistory(dm.user.id)
				if err != nil {
					showError = errors.New("error clearing chat history: " + err.Error())
					returnToThread = false
				}
			}
		},
		ui.mainWindow,
	)
	dialogCleanup := func() {
		if returnToThread {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
			ui.mainWindow.Canvas().Focus(dm.getEntry())
		}
		if showError != nil {
			ui.showDialog(dialog.NewError(showError, ui.mainWindow), nil)
		}
	}
	clearHistoryButton := widget.NewButton("Clear history", func() {
		ui.showDialog(confirmClearHistory, dialogCleanup)
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
			ui.bounce.SetDMMutedUntil(dm.user.id, mutedUntil)
		}

		// Update message retention if the selection doesn't line up with what we have for this user
		selectedRetentionString := dm.retentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			ui.showDialog(dialog.NewError(errors.New("invalid retention selection: "+selectedRetentionString), ui.mainWindow), nil)
			return
		} else {
			if dm.retention != selectedRetentionValue {
				ui.bounce.SetDMRetention(dm.user.id, selectedRetentionValue) // TODO: this should return an error if the user ID is nonsense
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
				ui.bounce.SetDMReadReceiptSettings(dm.user.id, false, true) // TODO: error check all of these
			} else if readReceiptSelection == on {
				ui.bounce.SetDMReadReceiptSettings(dm.user.id, true, true)
			} else if readReceiptSelection == off {
				ui.bounce.SetDMReadReceiptSettings(dm.user.id, true, false)
			} else {
				log.WithFields(log.Fields{
					"user_id":   dm.user.id,
					"selection": readReceiptSelection,
				}).Error("invalid selection for read receipt override")
			}
		}
		if switchedTypingIndicatorDefault || switchedTypingIndicatorEnabled {
			if defaultTypingIndicatorSelected {
				ui.bounce.SetDMTypingIndicatorSettings(dm.user.id, false, true)
			} else if typingIndicatorSelection == on {
				ui.bounce.SetDMTypingIndicatorSettings(dm.user.id, true, true)
			} else if typingIndicatorSelection == off {
				ui.bounce.SetDMTypingIndicatorSettings(dm.user.id, true, false)
			} else {
				log.WithFields(log.Fields{
					"user_id":   dm.user.id,
					"selection": typingIndicatorSelection,
				}).Error("invalid selection for typing indicator override")
			}
		}

		// Go back to the thread after settings updates are done
		ui.showMainContainer()
		ui.mainWindow.Canvas().Focus(dm.getEntry())
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
		dm.refreshReadReceiptSettingSelection(ui.readReceiptOverrideSelectionOptions())
		dm.refreshTypingIndicatorSettingSelection(ui.typingIndicatorOverrideSelectionOptions())

		// Show main tontainer
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
			ui.mainWindow.Canvas().Focus(dm.getEntry())
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

func (ui *ui) SetDMState(userID uuid.UUID, state chat.DMState) {
	// Find the thread
	dm, exists := ui.dms[userID]
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
		dm.refreshReadReceiptSettingSelection(ui.readReceiptOverrideSelectionOptions())
		dm.overrideTypingIndicatorSetting = state.OverrideTypingIndicatorSetting
		dm.typingIndicatorsEnabled = state.TypingIndicatorsEnabled
		dm.refreshTypingIndicatorSettingSelection(ui.typingIndicatorOverrideSelectionOptions())
	})
}

func (ui *ui) DMChatHistoryCleared(udch chat.UpdateDMClearHistory) {
	dmThread, err := ui.getOrCreateDM(udch.Thread)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"user_id": udch.Thread,
		}).Error("cannot clear history for unknown dm thread")
		return
	}

	ti, err := ui.newUpdateDMClearHistory(udch)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for clearing dm history")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(dmThread, ti) })
}

func (ui *ui) DMRetentionChanged(udr chat.UpdateDMRetention) {
	dmThread, err := ui.getOrCreateDM(udr.Thread)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"user_id": udr.Thread,
		}).Error("cannot update retention for unknown dm thread")
		return
	}

	ti, err := ui.newUpdateDMRetention(udr)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error creating thread item for update dm retention")
		return
	}

	fyne.DoAndWait(func() { ui.appendThreadItem(dmThread, ti) })
}

func (ui *ui) getOrCreateDM(id uuid.UUID) (*directMessage, error) {
	var dm *directMessage

	var dmExists bool
	dm, dmExists = ui.dms[id]
	if !dmExists {
		u, userExists := ui.users.get(id)
		if !userExists {
			return dm, errUnknownUser
		}

		fyne.DoAndWait(func() {
			ui.NewDirectMessage(chat.User{
				ID:   u.id,
				Name: u.getName(),
			})
		})
		dm, dmExists = ui.dms[id]
		if !dmExists {
			return dm, errors.New("direct message not found after creation")
		}
	}

	return dm, nil
}
