package ui

import (
	"errors"
	"fmt"
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
	notificationsMutedUntil          int64
	retention                        int64
	overrideReadReceiptSetting       bool
	readReceiptsEnabled              bool
	overrideTypingIndicatorSetting   bool
	typingIndicatorsEnabled          bool
	editIcon                         *defaultImage
	headerUsername                   *widget.Label
	headerIcon                       *defaultImage
	username                         *widget.Label
	realName                         *widget.RichText
	notesEntry                       *widget.Entry
	editContainer                    *fyne.Container
	view                             *fyne.Container
	header                           *fyne.Container
	button                           *threadButton
	typingIndicator                  *typingIndicator
	pendingMessageAttachments        *pendingMessageAttachments
	muteButton                       *widget.Button
	blockUserButton                  *widget.Button
	unblockUserButton                *widget.Button
	readReceiptOverrideSelection     *widget.Select
	typingIndicatorOverrideSelection *widget.Select
	scroll                           *chatHistory // TODO: rename
	entry                            *threadEntry
	retentionSelection               *widget.Select
	lastMessage                      int64
	opened                           bool
}

func (dm *directMessage) getID() uuid.UUID {
	return dm.user.id
}

func (dm *directMessage) getName() string {
	return dm.user.name
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

func (dm *directMessage) getEditIcon() *defaultImage {
	return dm.editIcon
}

func (dm *directMessage) getHeaderIcon() *defaultImage {
	return dm.headerIcon
}

func (dm *directMessage) setOpened() {
	dm.opened = true
}

func (dm *directMessage) hasBeenOpened() bool {
	return dm.opened
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
	dm, exists := ui.threads.getDM(userID)
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
	user, exists := ui.users.get(bounceUser.ID)
	if !exists {
		log.WithFields(log.Fields{
			"user_id": bounceUser.ID,
		}).Error("cannot create DM with user unknown to the UI")
		return
	}
	if _, exists := ui.threads.getDM(bounceUser.ID); exists {
		log.WithFields(log.Fields{
			"user_id": bounceUser.ID,
		}).Error("attempt to create a DM that already exists, ignoring")
		return
	}

	lastTimestamp := bounceUser.State.LastActivity
	if lastTimestamp == 0 {
		lastTimestamp = bounceUser.IntroductionTime
	}
	dm := &directMessage{
		user:                           user,
		notificationsMutedUntil:        bounceUser.State.MutedUntil,
		retention:                      bounceUser.State.Retention,
		lastMessage:                    lastTimestamp,
		overrideReadReceiptSetting:     bounceUser.State.OverrideReadReceiptSetting,
		readReceiptsEnabled:            bounceUser.State.ReadReceiptsEnabled,
		overrideTypingIndicatorSetting: bounceUser.State.OverrideTypingIndicatorSetting,
		typingIndicatorsEnabled:        bounceUser.State.TypingIndicatorsEnabled,
	}

	ui.buildEditDMContainer(dm)
	editButton := widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() {
		ui.showEditDMContainer(dm)
	})
	editButton.Importance = widget.LowImportance

	dm.headerIcon = newDefaultImage(user.id, bounceUser.Images, user.initials, 32, ui.bounce.GetFileData, nil) // TODO: get size from theme

	dm.headerUsername = widget.NewLabel(user.getDisplayName())
	dm.headerUsername.Truncation = fyne.TextTruncateEllipsis

	var userLabel *fyne.Container
	if fyne.CurrentDevice().IsMobile() {
		backButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
			ui.mobileBack()
		})
		backButton.Importance = widget.LowImportance

		leftItems := container.NewHBox(
			backButton,
			dm.headerIcon,
		)
		userLabel = container.New(
			layout.NewBorderLayout(nil, nil, leftItems, nil),
			leftItems,
			dm.headerUsername,
		)
	} else {
		userLabel = container.New(
			layout.NewBorderLayout(nil, nil, dm.headerIcon, nil),
			dm.headerIcon,
			dm.headerUsername,
		)
	}

	dm.headerUsername.TextStyle = fyne.TextStyle{Bold: true}
	dm.header = container.New(
		layout.NewBorderLayout(nil, nil, nil, editButton),
		editButton,
		userLabel,
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
		sources := map[uuid.UUID]string{}

		pendingAttachments := dm.pendingMessageAttachments.extract()
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

		chatDM.ImageAttachments = imageAttachments
		chatDM.FileAttachments = fileAttachments

		go ui.bounce.SendDirectMessage(chatDM, readers, sources)
	}

	openThread := func() {
		ui.displayThread(dm)
		ui.bounce.UserConnectionDesired(user.id)
		if fyne.CurrentDevice().IsMobile() {
			ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeThread, context: dm.user.id})
		}
		ui.state.currentView = viewTypeThread
	}
	dm.button = newThreadButton(newDefaultImage(user.id, bounceUser.Images, user.initials, 64, ui.bounce.GetFileData, openThread), user.getDisplayName(), openThread)
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

			err = dm.pendingMessageAttachments.add(reader)
			if err != nil {
				ui.showDialog(dialog.NewError(err, ui.window), nil)
			}
		}, ui.window).Show() // We do not use showDialog here because on mobile this uses a native intent
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
		container.New(&autoscrollLayout{}, dm.scroll),
	)
	ui.threads.add(bounceUser.ID, dm)
	dm.setLastMessageTime(dm.lastMessage)
	dm.button.setLastMessageTime(time.Unix(dm.lastMessage, 0))
	ui.refreshThreadOrder()
}

func (ui *ui) DisplayDirectMessage(dm chat.DirectMessage) {
	dmThread, reloaded, err := ui.getOrReloadDM(dm.Thread)
	if err != nil {
		log.Fatal("DM doesn't exist immediately after creation")
	}
	if reloaded {
		// The DM was closed and we opened it and reloaded all of this history,
		// which would have included this message
		return
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
	dmThread, reloaded, err := ui.getOrReloadDM(dm.Thread)
	if err != nil {
		log.Fatal("DM doesn't exist immediately after creation")
	}
	if reloaded {
		// The DM was closed and we opened it and reloaded all of this history,
		// which would have included this message
		return
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
		ui.containers.chat.Refresh()
	})
}

func (ui *ui) showEditDMContainer(dm *directMessage) {
	ui.setDMMutedUntilButton(dm)
	if dm.user.blocked {
		dm.blockUserButton.Hide()
		dm.unblockUserButton.Show()
	} else {
		dm.blockUserButton.Show()
		dm.unblockUserButton.Hide()
	}
	if dm.user.id == ui.state.profile.id {
		dm.blockUserButton.Hide()
		dm.unblockUserButton.Hide()
	}

	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeDMSettings, context: dm.user.id})
	}
	ui.state.currentView = viewTypeDMSettings
	ui.window.SetContent(dm.editContainer)
	dm.editContainer.Show()
}

func (ui *ui) buildEditDMContainer(dm *directMessage) {
	var selectImage func()
	if dm.user.id == ui.state.profile.id {
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
					ui.showDialog(dialog.NewError(errors.New("file is too large"), ui.window), nil)
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
			}, ui.window).Show() // We do not use showDialog here because on mobile this uses a native intent
		}
	}
	dm.editIcon = newDefaultImage(dm.user.id, dm.user.images, dm.user.initials, 128, ui.bounce.GetFileData, selectImage)

	dm.username = widget.NewLabel(dm.user.getDisplayName())
	dm.username.Truncation = fyne.TextTruncateEllipsis
	dm.username.Alignment = fyne.TextAlignCenter
	dm.realName = widget.NewRichTextWithText("(" + dm.user.name + ")")
	dm.realName.Truncation = fyne.TextTruncateEllipsis
	dm.realName.Segments[0].(*widget.TextSegment).Style = widget.RichTextStyle{
		SizeName:  theme.SizeNameCaptionText,
		Alignment: fyne.TextAlignCenter,
	}
	if len(dm.user.alias) > 0 && dm.user.alias != dm.user.name {
		dm.realName.Show()
	} else {
		dm.realName.Hide()
	}
	usernameEntry := widget.NewEntry()
	usernameEntry.Hide()
	nameEditStack := container.NewStack(
		dm.username,
		usernameEntry,
	)
	var nameSaveOrCancelButtons *fyne.Container
	var nameEditButton *widget.Button
	nameEditButton = widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		dm.username.Hide()
		usernameEntry.Text = dm.user.getDisplayName()
		usernameEntry.Show()
		ui.window.Canvas().Focus(usernameEntry)
		usernameEntry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEnd})
		nameEditButton.Hide()
		nameSaveOrCancelButtons.Show()
	})
	nameEditButton.Importance = widget.LowImportance
	nameSaveButton := widget.NewButtonWithIcon("", theme.DocumentSaveIcon(), func() {
		ui.bounce.AliasUser(dm.user.id, usernameEntry.Text)
		usernameEntry.Hide()
		nameSaveOrCancelButtons.Hide()
		dm.username.Show()
		nameEditButton.Show()
	})
	nameCancelButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		usernameEntry.Hide()
		nameSaveOrCancelButtons.Hide()
		dm.username.Show()
		nameEditButton.Show()
	})
	nameSaveOrCancelButtons = container.NewHBox(
		nameSaveButton,
		nameCancelButton,
	)
	nameSaveOrCancelButtons.Hide()
	buttonStack := container.NewStack(
		nameEditButton,
		nameSaveOrCancelButtons,
	)

	spacer := container.New(
		newMinWidthLayout(buttonStack.MinSize().Width),
		widget.NewLabel(""),
	)
	nameSection := container.NewVBox(
		container.New(
			layout.NewBorderLayout(nil, nil, spacer, buttonStack),
			spacer,
			buttonStack,
			nameEditStack,
		),
		dm.realName,
	)

	dm.muteButton = widget.NewButton("Mute", func() {})

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
		ui.window,
	)
	clearHistoryDialogCleanup := func() {
		if returnToThread {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
			ui.window.Canvas().Focus(dm.getEntry())
		}
		if showError != nil {
			ui.showDialog(dialog.NewError(showError, ui.window), nil)
		}
	}
	clearHistoryButton := widget.NewButton("Clear history", func() {
		ui.showDialog(confirmClearHistory, clearHistoryDialogCleanup)
	})

	hideThreadButton := widget.NewButton("Hide", func() {
		ui.threads.remove(dm.user.id)
		ui.containers.chat.Objects = []fyne.CanvasObject{ui.containers.defaultContainer}
		ui.containers.chat.Refresh()
		ui.state.activeThread = uuid.Nil
		ui.refreshThreadOrder()
		dm.opened = false
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack() // Exit the edit DM menu
			ui.mobileBack() // Exit the thread back to all threads
		} else {
			ui.showMainContainer()
		}
		ui.bounce.SetOpenDM(dm.user.id, false)
	})

	blockUserDialogCleanup := func() {
		if returnToThread {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
		}
		if showError != nil {
			ui.showDialog(dialog.NewError(showError, ui.window), nil)
		}
	}
	dm.blockUserButton = widget.NewButton("Block", func() {
		groupsWarning := ""
		groupsToLeave := ui.threads.groupsWithUser(dm.user.id)
		if groupsToLeave == 1 {
			groupsWarning = "  You will be removed from 1 group you have in common."
		} else if groupsToLeave > 1 {
			groupsWarning = fmt.Sprintf("  You will be removed from %d groups you have in common.", groupsToLeave)
		}

		confirmBlockUser := dialog.NewConfirm(
			fmt.Sprintf("Block %s?", dm.user.getDisplayName()),
			"Are you sure you want to block this user?"+groupsWarning,
			func(confirmed bool) {
				returnToThread = confirmed
				showError = nil
				if confirmed {
					err := ui.bounce.BlockUser(dm.user.id)
					if err != nil {
						showError = errors.New("error blocking user: " + err.Error())
						returnToThread = false
					}
				}
			},
			ui.window,
		)
		ui.showDialog(confirmBlockUser, blockUserDialogCleanup)
	})
	unblockUserDialogCleanup := func() {
		if returnToThread {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
			ui.window.Canvas().Focus(dm.getEntry())
		}
		if showError != nil {
			ui.showDialog(dialog.NewError(showError, ui.window), nil)
		}
	}
	dm.unblockUserButton = widget.NewButton("Unblock", func() {
		confirmUnblockUser := dialog.NewConfirm(
			fmt.Sprintf("Unblock %s?", dm.user.getDisplayName()),
			"Are you sure you want to unblock this user?",
			func(confirmed bool) {
				returnToThread = confirmed
				showError = nil
				if confirmed {
					err := ui.bounce.UnblockUser(dm.user.id)
					if err != nil {
						showError = errors.New("error unblocking user: " + err.Error())
						returnToThread = false
					}
				}
			},
			ui.window,
		)
		ui.showDialog(confirmUnblockUser, unblockUserDialogCleanup)
	})
	if dm.user.blocked {
		dm.blockUserButton.Hide()
	} else {
		dm.unblockUserButton.Hide()
	}
	if dm.user.id == ui.state.profile.id {
		dm.blockUserButton.Hide()
		dm.unblockUserButton.Hide()
	}

	dm.notesEntry = widget.NewMultiLineEntry()
	dm.notesEntry.SetText(dm.user.notes)
	dm.notesEntry.Disable()
	notesLabel := widget.NewLabel("Notes")
	notesLabelSection := container.New(
		layout.NewBorderLayout(notesLabel, nil, nil, nil),
		notesLabel,
	)

	var editNotesButton *widget.Button
	var saveOrCancelNotesButtons *fyne.Container
	editNotesButton = widget.NewButtonWithIcon("", theme.DocumentCreateIcon(), func() {
		dm.notesEntry.Enable()
		ui.window.Canvas().Focus(dm.notesEntry)
		dm.notesEntry.TypedKey(&fyne.KeyEvent{Name: fyne.KeyEnd})
		editNotesButton.Hide()
		saveOrCancelNotesButtons.Show()
	})
	editNotesButton.Importance = widget.LowImportance
	saveNotesButton := widget.NewButtonWithIcon("", theme.DocumentSaveIcon(), func() {
		ui.bounce.SetUserNotes(dm.user.id, dm.notesEntry.Text)
		dm.notesEntry.Disable()
		saveOrCancelNotesButtons.Hide()
		editNotesButton.Show()
	})
	cancelNotesButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		dm.notesEntry.SetText(dm.user.notes)
		dm.notesEntry.Disable()
		saveOrCancelNotesButtons.Hide()
		editNotesButton.Show()
	})
	saveOrCancelNotesButtons = container.NewHBox(
		saveNotesButton,
		cancelNotesButton,
	)
	saveOrCancelNotesButtons.Hide()
	notesButtons := container.NewStack(
		editNotesButton,
		saveOrCancelNotesButtons,
	)
	notesButtonsSection := container.New(
		layout.NewBorderLayout(nil, nil, nil, notesButtons),
		notesButtons,
	)
	notes := container.NewVBox(
		container.New(
			layout.NewBorderLayout(nil, nil, notesLabelSection, nil),
			notesLabelSection,
			dm.notesEntry,
		),
		notesButtonsSection,
	)

	//
	// Save and Cancel buttons
	//

	saveButton := widget.NewButton("Save", func() {
		// Update message retention if the selection doesn't line up with what we have for this user
		selectedRetentionString := dm.retentionSelection.Selected
		selectedRetentionValue, ok := retentionValues[selectedRetentionString]
		if !ok {
			ui.showDialog(dialog.NewError(errors.New("invalid retention selection: "+selectedRetentionString), ui.window), nil)
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

		// Do the alias saving, if it's being edited
		if nameSaveOrCancelButtons.Visible() {
			ui.bounce.AliasUser(dm.user.id, usernameEntry.Text)
			usernameEntry.Hide()
			nameSaveOrCancelButtons.Hide()
			dm.username.Show()
			nameEditButton.Show()
		}

		if saveOrCancelNotesButtons.Visible() {
			ui.bounce.SetUserNotes(dm.user.id, dm.notesEntry.Text)
			dm.notesEntry.Disable()
			saveOrCancelNotesButtons.Hide()
			editNotesButton.Show()
		}

		// Go back to the thread after settings updates are done
		ui.showMainContainer()
		ui.window.Canvas().Focus(dm.getEntry())
	})
	saveButton.Importance = widget.HighImportance

	cancelChanges := func() {
		// Reset retention
		dm.retentionSelection.Selected = getRetentionName(dm.retention)
		dm.retentionSelection.Refresh()

		// Reset the read receipt and typing indicator overrides
		dm.refreshReadReceiptSettingSelection(ui.readReceiptOverrideSelectionOptions())
		dm.refreshTypingIndicatorSettingSelection(ui.typingIndicatorOverrideSelectionOptions())

		// Cancel alias and notes if they are being edited
		if nameSaveOrCancelButtons.Visible() {
			usernameEntry.Hide()
			nameSaveOrCancelButtons.Hide()
			dm.username.Show()
			nameEditButton.Show()
		}

		if saveOrCancelNotesButtons.Visible() {
			dm.notesEntry.SetText(dm.user.notes)
			dm.notesEntry.Disable()
			saveOrCancelNotesButtons.Hide()
			editNotesButton.Show()
		}

		// Show main tontainer
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
			ui.window.Canvas().Focus(dm.getEntry())
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
		nameSection,
		widget.NewLabel("Disappearing Messages"),
		dm.retentionSelection,
		container.NewHBox(
			dm.muteButton,
			clearHistoryButton,
			hideThreadButton,
			dm.blockUserButton,
			dm.unblockUserButton,
		),
		notes,
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
	// Find the user and thread if it already opened
	u, ok := ui.users.get(userID)
	if !ok {
		log.WithFields(log.Fields{
			"user_id": userID,
		}).Error("cannot set state of DM for unknown user")
		return
	}
	dm, alreadyOpen := ui.threads.getDM(userID)
	if !alreadyOpen {
		if !state.Open {
			// The DM isn't opened and we've already closed it
			return
		}
	}

	fyne.DoAndWait(func() {
		if state.Open && !alreadyOpen {
			dm = ui.openAndPopulateDM(u)
			if dm == nil {
				log.Error("cannot create dm")
				return
			}
			ui.refreshThreadOrder()
		} else if !state.Open && alreadyOpen {
			ui.threads.remove(dm.user.id)
			if ui.state.activeThread == dm.user.id {
				ui.containers.chat.Objects = []fyne.CanvasObject{ui.containers.defaultContainer}
				ui.containers.chat.Refresh()
				ui.state.activeThread = uuid.Nil
			}
			ui.refreshThreadOrder()
			return
		}

		dm.notificationsMutedUntil = state.MutedUntil
		ui.setDMMutedUntilButton(dm)

		// Update retention
		dm.retention = state.Retention
		newRetentionName := getRetentionName(dm.retention)
		dm.retentionSelection.Selected = newRetentionName
		dm.retentionSelection.Refresh()

		// Update read receipt and typign indicator selections
		dm.overrideReadReceiptSetting = state.OverrideReadReceiptSetting
		dm.readReceiptsEnabled = state.ReadReceiptsEnabled
		dm.refreshReadReceiptSettingSelection(ui.readReceiptOverrideSelectionOptions())
		dm.overrideTypingIndicatorSetting = state.OverrideTypingIndicatorSetting
		dm.typingIndicatorsEnabled = state.TypingIndicatorsEnabled
		dm.refreshTypingIndicatorSettingSelection(ui.typingIndicatorOverrideSelectionOptions())
	})
}

func (ui *ui) setDMMutedUntilButton(dm *directMessage) {
	if dm.notificationsMutedUntil == chat.MutedForever {
		dm.muteButton.Text = "Unmute"
		dm.muteButton.Icon = nil
		dm.muteButton.OnTapped = func() {
			err := ui.bounce.SetDMMutedUntil(dm.user.id, 0)
			if err != nil {
				ui.showDialog(dialog.NewError(errors.New("error updating group mute settings: "+err.Error()), ui.window), nil)
			}
		}
	} else if dm.notificationsMutedUntil == 0 || dm.notificationsMutedUntil < time.Now().Unix() {
		dm.muteButton.Text = "Mute"
		dm.muteButton.Icon = nil
		dm.muteButton.OnTapped = func() {
			var muteMenu dialog.Dialog
			content := container.NewVBox(
				widget.NewButton("Mute for 5 mins", func() {
					err := ui.bounce.SetDMMutedUntil(dm.user.id, time.Now().Unix()+60*5)
					if err != nil {
						ui.showDialog(dialog.NewError(errors.New("error updating group mute settings: "+err.Error()), ui.window), nil)
					}
					muteMenu.Dismiss()
				}),
				widget.NewButton("Mute for 1 hour", func() {
					err := ui.bounce.SetDMMutedUntil(dm.user.id, time.Now().Unix()+60*60)
					if err != nil {
						ui.showDialog(dialog.NewError(errors.New("error updating group mute settings: "+err.Error()), ui.window), nil)
					}
					muteMenu.Dismiss()
				}),
				widget.NewButton("Mute for 1 day", func() {
					err := ui.bounce.SetDMMutedUntil(dm.user.id, time.Now().Unix()+60*60*24)
					if err != nil {
						ui.showDialog(dialog.NewError(errors.New("error updating group mute settings: "+err.Error()), ui.window), nil)
					}
					muteMenu.Dismiss()
				}),
				widget.NewButton("Mute for 1 week", func() {
					err := ui.bounce.SetDMMutedUntil(dm.user.id, time.Now().Unix()+60*60*24*7)
					if err != nil {
						ui.showDialog(dialog.NewError(errors.New("error updating group mute settings: "+err.Error()), ui.window), nil)
					}
					muteMenu.Dismiss()
				}),
				widget.NewButton("Mute forever", func() {
					err := ui.bounce.SetDMMutedUntil(dm.user.id, chat.MutedForever)
					if err != nil {
						ui.showDialog(dialog.NewError(errors.New("error updating group mute settings: "+err.Error()), ui.window), nil)
					}
					muteMenu.Dismiss()
				}),
				widget.NewButton("Cancel", func() {
					muteMenu.Dismiss()
				}),
			)
			muteMenu = dialog.NewCustomWithoutButtons("Mute", content, ui.window)
			ui.showDialog(muteMenu, nil)
		}
	} else {
		dm.muteButton.Text = "Muted"
		dm.muteButton.Icon = theme.CalendarIcon()
		dm.muteButton.OnTapped = func() {
			var muteMenu dialog.Dialog
			content := container.NewHBox(
				widget.NewLabel("Muted until "+mutedTimestampString(dm.notificationsMutedUntil)),
				widget.NewButton("Unmute Now", func() {
					err := ui.bounce.SetDMMutedUntil(dm.user.id, 0)
					if err != nil {
						ui.showDialog(dialog.NewError(errors.New("error updating group mute settings: "+err.Error()), ui.window), nil)
					}
					muteMenu.Dismiss()
				}),
				widget.NewButton("Cancel", func() {
					muteMenu.Dismiss()
				}),
			)
			muteMenu = dialog.NewCustomWithoutButtons("Mute", content, ui.window)
			ui.showDialog(muteMenu, nil)

		}
	}
	dm.muteButton.Refresh()
}

func (ui *ui) DMChatHistoryCleared(udch chat.UpdateDMClearHistory) {
	dmThread, reloaded, err := ui.getOrReloadDM(udch.Thread)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"user_id": udch.Thread,
		}).Error("cannot clear history for unknown dm thread")
		return
	}
	if reloaded {
		// The DM was closed and we opened it and reloaded all of this history,
		// which would have included this update
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
	dmThread, reloaded, err := ui.getOrReloadDM(udr.Thread)
	if err != nil {
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"user_id": udr.Thread,
		}).Error("cannot update retention for unknown dm thread")
		return
	}
	if reloaded {
		// The DM was closed and we opened it and reloaded all of this history,
		// which would have included this update
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

func (ui *ui) getOrReloadDM(id uuid.UUID) (*directMessage, bool, error) {
	reloaded := false
	dm, ok := ui.threads.getDM(id)
	if !ok {
		u, ok := ui.users.get(id)
		if !ok {
			return nil, false, errUnknownUser
		}
		fyne.DoAndWait(func() { ui.openAndPopulateDM(u) })
		dm, ok = ui.threads.getDM(id)
		ui.bounce.SetOpenDM(u.id, true)
		reloaded = true
	}

	return dm, reloaded, nil
}

func (ui *ui) openAndPopulateDM(u *user) *directMessage {
	state := ui.bounce.GetDMHistory(u.id)
	if len(state.Users) != 1 {
		log.Error("loading DM history did not return state with one user")
		return nil
	}
	chatUser := state.Users[0]

	ui.NewDirectMessage(chatUser)
	dm, ok := ui.threads.getDM(u.id)
	if !ok {
		log.Fatal("DM doesn't exist immediately after creation")
	}

	tis := []*threadItem{}
	for _, dm := range state.DirectMessages {
		dmti, err := ui.newDirectMessage(dm)
		if err != nil {
			log.Error(err.Error())
		} else {
			tis = append(tis, dmti)
		}
	}
	for _, udmr := range state.UpdateDMRetentions {
		udmrItem, err := ui.newUpdateDMRetention(udmr)
		if err != nil {
			log.Error(err.Error())
		} else {
			tis = append(tis, udmrItem)
		}
	}

	for _, udmch := range state.UpdateDMClearHistories {
		udmchItem, err := ui.newUpdateDMClearHistory(udmch)
		if err != nil {
			log.Error(err.Error())
		} else {
			tis = append(tis, udmchItem)
		}
	}
	for _, uuun := range state.UpdateUserUpdateNames {
		uuunItem, err := ui.userChangedName(uuun.ID, uuun.User, uuun.OldName, uuun.Name, uuun.Timestamp)
		if err != nil {
			log.Error(err.Error())
		} else {
			tis = append(tis, uuunItem)
		}
	}
	for _, uuui := range state.UpdateUserUpdateImages {
		uuuiItem, err := ui.userChangedImage(uuui.ID, uuui.User, uuui.Timestamp)
		if err != nil {
			log.Error(err.Error())
		} else {
			tis = append(tis, uuuiItem)
		}
	}
	ui.populateInitialItems(dm, tis)
	ui.threads.add(u.id, dm)
	ui.refreshThreadOrder()
	for _, fp := range state.FileProgress {
		ui.FileDownloadProgress(fp.ID, fp.Progress)
	}

	return dm
}
