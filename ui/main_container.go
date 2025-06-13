package ui

import (
	"errors"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	//xwidget "fyne.io/x/fyne/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

func (ui *ui) showMainContainer() {
	if !ui.initialStateSet { // TODO: rename?
		ui.mainWindow.SetMainMenu(nil)
		ui.mainWindow.SetContent(ui.databaseLoading)
		ui.databaseLoading.Show()
	} else if ui.profile == nil {
		// The database has been loaded but this is a new install with no profile setup.
		// Display the screen that the user is on for walking through new device setup.
		if ui.setupStep == setupStepInit {
			ui.mainWindow.SetMainMenu(nil)
			ui.mainWindow.SetContent(ui.newInstall)
			ui.newInstall.Show()
			ui.viewStack = []view{view{viewType: viewTypeNewInstall}}
		} else if ui.setupStep == setupStepProfile {
			ui.mainWindow.SetMainMenu(nil)
			ui.mainWindow.SetContent(ui.newProfileCreator)
			ui.newProfileCreator.Show()
			if fyne.CurrentDevice().IsMobile() {
				ui.viewStack = append(ui.viewStack, view{viewType: viewTypeProfileCreator})
			}
		} else {
			log.WithFields(log.Fields{
				"step": ui.setupStep,
			}).Fatal("unconfigured user interface is in unknown setup step")
		}
	} else {
		ui.viewStack = []view{view{viewType: viewTypeAllThreads}}
		ui.mainWindow.SetMainMenu(ui.mainMenu)
		ui.mainWindow.SetContent(ui.mainContainer)
		ui.mainContainer.Show()
	}
}

func (ui *ui) buildMainContainer() {
	// Logo and welcome message / instructions to be shown before a thread is selected
	ui.defaultContainer = container.NewMax(
		container.New(
			layout.NewCenterLayout(),
			container.NewVBox(
				makeLogo(228, 167), // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
				widget.NewLabel("Select a thread on the left to get started"),
			),
		),
	)
	ui.chatContainer = container.NewMax()
	ui.chatContainer.Objects = []fyne.CanvasObject{ui.defaultContainer}
	ui.threadVBox = container.NewVBox()

	threadSearch := widget.NewButtonWithIcon("", theme.SearchIcon(), func() {
		// TODO: replace the top bar with a search entry for n-gram search of thread names, switch back to
		// icon, title, and search icon when entry loses focus
	})
	threadSearch.Importance = widget.LowImportance

	topButtons := container.NewHBox(threadSearch)

	icon := canvas.NewImageFromResource(newEmbeddedResource("assets/icon.png"))
	icon.FillMode = canvas.ImageFillContain
	icon.SetMinSize(fyne.NewSize(theme.TextHeadingSize(), theme.TextHeadingSize()))

	if fyne.CurrentDevice().IsMobile() {
		logoWithText := container.NewHBox(
			icon,
			widget.NewRichText(&widget.TextSegment{
				Style: widget.RichTextStyle{
					SizeName: theme.SizeNameHeadingText,
				},
				Text: "Bounce",
			}),
		)

		logoSearchAndMenu := container.New(
			layout.NewBorderLayout(nil, nil, logoWithText, topButtons),
			logoWithText,
			topButtons,
			ui.networkOfflineWarning,
		)

		settings := widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() {
			ui.showMobileMenu()
		})
		settings.Importance = widget.LowImportance
		topButtons.Objects = append(topButtons.Objects, settings)

		ui.mainContainer = container.New(
			layout.NewBorderLayout(logoSearchAndMenu, nil, nil, nil),
			logoSearchAndMenu,
			container.NewVScroll(ui.threadVBox),
		)
	} else {
		logoSearchAndMenu := container.New(
			layout.NewBorderLayout(nil, nil, nil, topButtons),
			topButtons,
			container.New(
				layout.NewBorderLayout(nil, nil, icon, nil),
				icon,
				ui.networkOfflineWarning,
			),
		)

		threads := container.NewHBox(
			container.New(
				layout.NewBorderLayout(nil, logoSearchAndMenu, nil, nil),
				logoSearchAndMenu,
				container.NewVScroll(ui.threadVBox),
			),
			widget.NewSeparator(),
		)
		ui.mainContainer = container.New(
			layout.NewBorderLayout(nil, nil, threads, nil),
			threads,
			ui.chatContainer,
		)
	}
}

func (ui *ui) buildDatabaseLoading() {
	//animation, err := xwidget.NewAnimatedGifFromResource(newEmbeddedResource("assets/icon-animated.gif"))
	//if err != nil {
	//	log.WithFields(log.Fields{
	//		"error": err.Error(),
	//	}).Fatal("error creating gif")
	//}
	//animation.SetMinSize(fyne.NewSize(200, 200))
	//animation.Start()

	//progress := widget.NewProgressBarInfinite()
	//progress.Start()

	loading := widget.NewLabel("loading...")
	loading.Alignment = fyne.TextAlignCenter

	ui.databaseLoading = container.NewMax(
		container.New(
			layout.NewCenterLayout(),
			container.NewVBox(
				makeLogo(228, 167), // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
				//progress,
				loading,
			),
		),
	)
}

func (ui *ui) buildNewInstall() {
	header := container.NewVBox(
		container.NewCenter(makeLogo(228, 167)), // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
	)

	selectNewProfile := newClickableImage("Create a new profile", canvas.NewImageFromResource(newEmbeddedResource("assets/new_profile.png")), 200, 200, true, func() {
		ui.setupStep = setupStepProfile
		ui.mainWindow.SetContent(ui.newProfileCreator)
		ui.newProfileCreator.Show()
	})

	selectSyncDevice := newClickableImage("Add device to profile", canvas.NewImageFromResource(newEmbeddedResource("assets/add_to_profile.png")), 200, 200, true, func() {
		ui.mainWindow.SetContent(ui.nameNewDevice)
		ui.nameNewDevice.Show()
	})

	content := container.NewGridWithColumns(
		1,
		selectNewProfile,
		selectSyncDevice,
	)

	ui.newInstall = container.New(
		layout.NewBorderLayout(header, nil, nil, nil),
		header,
		content,
	)
}

func (ui *ui) buildNewProfileCreator() {
	profileNameEntry := widget.NewEntry()
	userIconName := binding.NewString()

	profileNameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		profileNameEntry.Text = str
		if trimmed != 0 {
			profileNameEntry.CursorRow = 0
			profileNameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			profileNameEntry.Text = string(truncated)
		}
		profileNameEntry.Refresh()

		// Set the user icon
		parts := strings.Split(str, " ")
		if len(parts) == 1 {
			r, _ := utf8.DecodeRuneInString(parts[0])
			if r == utf8.RuneError {
				userIconName.Set("")
				return
			}

			userIconName.Set(string(r))
		} else {
			firstRune, _ := utf8.DecodeRuneInString(parts[0])
			if firstRune == utf8.RuneError {
				userIconName.Set("")
				return
			}

			lastRune, _ := utf8.DecodeRuneInString(parts[len(parts)-1])
			if lastRune == utf8.RuneError {
				userIconName.Set(string(firstRune))
				return
			}

			userIconName.Set(string(firstRune) + string(lastRune))
		}
	}

	ui.newProfileImageData = []byte{}
	fileGetter := func(_ uuid.UUID) ([]byte, error) {
		return ui.newProfileImageData, nil
	}
	ui.newProfileImage = newDefaultImage(uuid.Nil, []uuid.UUID{}, userIconName, 128, fileGetter, func() {
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

			ui.newProfileImageData = data
			ui.newProfileImage.images = []uuid.UUID{uuid.New()}
			ui.newProfileImage.Refresh()
		}, ui.mainWindow).Show() // We do not use showDialog here because on mobile this uses a native intent
	})

	deviceNameEntry := widget.NewEntry()
	hostname, err := os.Hostname()
	if err == nil {
		deviceNameEntry.PlaceHolder = hostname
	}
	deviceNameEntry.OnChanged = func(str string) {
		// Remove any leading whitespace
		str, trimmed := trimLeadingSpace(str)
		deviceNameEntry.Text = str
		if trimmed != 0 {
			deviceNameEntry.CursorRow = 0
			deviceNameEntry.CursorColumn = 0
		}

		// Enforce length limit
		if utf8.RuneCountInString(str) > chat.MaximumNameLength {
			runes := []rune(str)
			truncated := runes[0:chat.MaximumNameLength]
			deviceNameEntry.Text = string(truncated)
		}
		deviceNameEntry.Refresh()
	}

	profileForm := widget.NewForm(
		&widget.FormItem{
			Text:   "Your Name:",
			Widget: profileNameEntry,
		},
		&widget.FormItem{
			Text:     "Device Name:",
			Widget:   deviceNameEntry,
			HintText: "\"Phone\", \"Laptop\", etc",
		},
	)

	profileDetails := container.NewVBox(
		container.NewCenter(ui.newProfileImage),
		profileForm,
	)

	saveButton := widget.NewButton("Save", func() {
		if profileNameEntry.Text == "" {
			ui.showDialog(dialog.NewError(errors.New("Profile name must be set"), ui.mainWindow), nil)
			return
		}
		if deviceNameEntry.Text == "" {
			ui.showDialog(dialog.NewError(errors.New("Device name must be set"), ui.mainWindow), nil)
			return
		}
		id, imageID, err := ui.bounce.SetProfile(profileNameEntry.Text, ui.newProfileImageData, deviceNameEntry.Text)
		if err != nil {
			ui.showDialog(dialog.NewError(errors.New("Error saving profile: "+err.Error()), ui.mainWindow), nil)
			return
		}
		profile := makeUser(id, profileNameEntry.Text)
		ui.users.add(profile)
		ui.profile = profile
		if imageID != uuid.Nil {
			ui.profile.images = []uuid.UUID{imageID}
			ui.profileIcon.images = ui.profile.images
			ui.profileIcon.Refresh()
		}
		ui.showMainContainer()
	})
	saveButton.Importance = widget.HighImportance
	backButton := widget.NewButton("Back", func() {
		profileNameEntry.Text = ""
		profileNameEntry.Refresh()
		userIconName.Set("")
		deviceNameEntry.Text = ""
		deviceNameEntry.Refresh()
		ui.newProfileImageData = []byte{}
		ui.newProfileImage.images = []uuid.UUID{}
		ui.newProfileImage.Refresh()
		ui.setupStep = setupStepInit
		ui.mainWindow.SetContent(ui.newInstall)
		ui.newInstall.Show()
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, backButton, saveButton),
		saveButton,
		backButton,
	)

	ui.newProfileCreator = container.New(
		layout.NewBorderLayout(nil, actionButtons, nil, nil),
		actionButtons,
		container.NewMax(profileDetails),
	)
}

func makeLogo(width, height float32) *themedImage {
	dark := newEmbeddedResource("assets/logo_dark.png")
	light := newEmbeddedResource("assets/logo.png")

	return newThemedImage(dark, light, width, height)
}
