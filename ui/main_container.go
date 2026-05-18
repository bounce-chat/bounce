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
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	//xwidget "fyne.io/x/fyne/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

type newInstall struct {
	newProfileImage     *defaultImage
	newProfileImageData []byte
}

func (ui *ui) showMainContainer() {
	if !ui.state.initialStateSet { // TODO: rename?
		ui.window.SetMainMenu(nil)
		ui.window.SetContent(ui.views.databaseLoading)
	} else if ui.state.profile == nil {
		// The database has been loaded but this is a new install with no profile setup.
		// Display the screen that the user is on for walking through new device setup.
		if ui.state.setupStep == setupStepInit {
			ui.window.SetMainMenu(nil)
			ui.window.SetContent(ui.views.newInstall)
			ui.state.viewStack = []view{view{viewType: viewTypeNewInstall}}
			ui.state.currentView = viewTypeNewInstall
		} else if ui.state.setupStep == setupStepProfile {
			ui.window.SetMainMenu(nil)
			ui.window.SetContent(ui.views.newProfileCreator)
			if fyne.CurrentDevice().IsMobile() {
				ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeProfileCreator})
			}
			ui.state.currentView = viewTypeProfileCreator
		} else {
			log.WithFields(log.Fields{
				"step": ui.state.setupStep,
			}).Fatal("unconfigured user interface is in unknown setup step")
		}
	} else {
		ui.state.viewStack = []view{view{viewType: viewTypeAllThreads}}
		ui.state.currentView = viewTypeAllThreads
		ui.window.SetMainMenu(ui.containers.mainMenu)
		ui.window.SetContent(ui.views.main)
	}
}

func (ui *ui) buildMainContainer() {
	// Logo and welcome message / instructions to be shown before a thread is selected
	ui.containers.defaultContainer = container.NewMax(
		container.New(
			layout.NewCenterLayout(),
			container.NewVBox(
				makeLogo(228, 167), // TODO: choose reasonable values here, https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
				widget.NewLabel("Select a thread on the left to get started"),
			),
		),
	)
	ui.containers.chat = container.NewMax()
	ui.containers.chat.Objects = []fyne.CanvasObject{ui.containers.defaultContainer}
	ui.containers.threads = container.NewVBox()

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
			ui.widgets.networkOfflineWarning,
		)

		settings := widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() {
			ui.showMobileMenu()
		})
		settings.Importance = widget.LowImportance
		topButtons.Objects = append(topButtons.Objects, settings)

		ui.views.main = container.New(
			layout.NewBorderLayout(logoSearchAndMenu, nil, nil, nil),
			logoSearchAndMenu,
			container.NewVScroll(ui.containers.threads),
		)
	} else {
		logoSearchAndMenu := container.New(
			layout.NewBorderLayout(nil, nil, nil, topButtons),
			topButtons,
			container.New(
				layout.NewBorderLayout(nil, nil, icon, nil),
				icon,
				ui.widgets.networkOfflineWarning,
			),
		)

		threads := container.NewHBox(
			container.New(
				layout.NewBorderLayout(nil, logoSearchAndMenu, nil, nil),
				logoSearchAndMenu,
				container.NewVScroll(ui.containers.threads),
			),
			widget.NewSeparator(),
		)
		ui.views.main = container.New(
			layout.NewBorderLayout(nil, nil, threads, nil),
			threads,
			ui.containers.chat,
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

	ui.views.databaseLoading = container.NewMax(
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

	selectNewProfile := newClickableImage("Create a new profile", canvas.NewImageFromResource(newEmbeddedResource("assets/icons/setup/new_profile.png")), 200, 200, true, func() {
		ui.state.setupStep = setupStepProfile
		ui.window.SetContent(ui.views.newProfileCreator)
		ui.views.newProfileCreator.Show()
	})

	selectSyncDevice := newClickableImage("Add device to profile", canvas.NewImageFromResource(newEmbeddedResource("assets/icons/setup/add_to_profile.png")), 200, 200, true, func() {
		ui.window.SetContent(ui.views.nameNewDevice)
	})

	content := container.NewGridWithColumns(
		1,
		selectNewProfile,
		selectSyncDevice,
	)

	ui.views.newInstall = container.New(
		layout.NewBorderLayout(header, nil, nil, nil),
		header,
		content,
	)
}

func (ui *ui) buildNewProfileCreator() {
	ui.widgets.newInstall = &newInstall{}

	profileNameEntry := widget.NewEntry()

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
				ui.widgets.newInstall.newProfileImage.setString("")
				return
			}

			ui.widgets.newInstall.newProfileImage.setString(string(r))
		} else {
			firstRune, _ := utf8.DecodeRuneInString(parts[0])
			if firstRune == utf8.RuneError {
				ui.widgets.newInstall.newProfileImage.setString("")
				return
			}

			lastRune, _ := utf8.DecodeRuneInString(parts[len(parts)-1])
			if lastRune == utf8.RuneError {
				ui.widgets.newInstall.newProfileImage.setString(string(firstRune))
				return
			}

			ui.widgets.newInstall.newProfileImage.setString(string(firstRune) + string(lastRune))
		}
	}

	ui.widgets.newInstall.newProfileImageData = []byte{}
	fileGetter := func(id uuid.UUID) ([]byte, error) {
		return ui.widgets.newInstall.newProfileImageData, nil
	}
	ui.widgets.newInstall.newProfileImage = newDefaultImage(uuid.Nil, []uuid.UUID{}, "", 128, fileGetter, func() {
		dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
			if reader == nil {
				return
			}

			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Debug("error selecting image for new profile")
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

			ui.widgets.newInstall.newProfileImageData = data
			ui.widgets.newInstall.newProfileImage.images = []uuid.UUID{uuid.New()}
			ui.widgets.newInstall.newProfileImage.Refresh()
			ui.views.newProfileCreator.Refresh()
		}, ui.window).Show() // We do not use showDialog here because on mobile this uses a native intent
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
		container.NewCenter(ui.widgets.newInstall.newProfileImage),
		profileForm,
	)

	saveButton := widget.NewButton("Save", func() {
		if profileNameEntry.Text == "" {
			ui.showDialog(dialog.NewError(errors.New("Profile name must be set"), ui.window), nil)
			return
		}

		if deviceNameEntry.Text == "" {
			deviceNameEntry.Text = deviceNameEntry.PlaceHolder
		}
		if deviceNameEntry.Text == "" {
			ui.showDialog(dialog.NewError(errors.New("Device name must be set"), ui.window), nil)
			return
		}
		err := ui.bounce.SetProfile(profileNameEntry.Text, ui.widgets.newInstall.newProfileImageData, deviceNameEntry.Text)
		if err != nil {
			ui.showDialog(dialog.NewError(errors.New("Error saving profile: "+err.Error()), ui.window), nil)
			return
		}
	})
	saveButton.Importance = widget.HighImportance
	backButton := widget.NewButton("Back", func() {
		profileNameEntry.Text = ""
		profileNameEntry.Refresh()
		ui.widgets.newInstall.newProfileImage.setString("")
		deviceNameEntry.Text = ""
		deviceNameEntry.Refresh()
		ui.widgets.newInstall.newProfileImageData = []byte{}
		ui.widgets.newInstall.newProfileImage.images = []uuid.UUID{}
		ui.widgets.newInstall.newProfileImage.Refresh()
		ui.state.setupStep = setupStepInit
		ui.window.SetContent(ui.views.newInstall)
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, backButton, saveButton),
		saveButton,
		backButton,
	)

	ui.views.newProfileCreator = container.New(
		layout.NewBorderLayout(nil, actionButtons, nil, nil),
		actionButtons,
		container.NewMax(profileDetails),
	)
}

func (ui *ui) ProfileSet(u chat.User, d chat.Device) {
	fyne.DoAndWait(func() {
		profile := makeUser(u)
		ui.users.add(profile)
		ui.state.profile = profile
		ui.widgets.editProfile.profileIcon.images = ui.state.profile.images
		ui.showMainContainer()
		ui.NewDirectMessage(u)

		ui.devices.add(&d)
		ui.updateDeviceStatus()
	})
}

func makeLogo(width, height float32) *themedImage {
	dark := newEmbeddedResource("assets/logo_dark.png")
	light := newEmbeddedResource("assets/logo.png")

	return newThemedImage(dark, light, width, height)
}
