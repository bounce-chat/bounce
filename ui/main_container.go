package ui

import (
	"errors"
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
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
)

func (fyneUI *Fyne) showMainContainer() {
	if !fyneUI.initialStateSet { // TODO: rename?
		fyneUI.mainWindow.SetMainMenu(nil)
		fyneUI.mainWindow.SetContent(fyneUI.databaseLoading)
		fyneUI.databaseLoading.Show()
	} else if fyneUI.profile == nil {
		// The database has been loaded but this is a new install with no profile setup.
		// Display the screen that the user is on for walking through new device setup.
		if fyneUI.setupStep == setupStepInit {
			fyneUI.mainWindow.SetMainMenu(nil)
			fyneUI.mainWindow.SetContent(fyneUI.newInstall)
			fyneUI.newInstall.Show()
		} else if fyneUI.setupStep == setupStepProfile {
			fyneUI.mainWindow.SetMainMenu(nil)
			fyneUI.mainWindow.SetContent(fyneUI.newProfileCreator)
			fyneUI.newProfileCreator.Show()
		} else {
			log.WithFields(log.Fields{
				"step": fyneUI.setupStep,
			}).Fatal("unconfigured user interface is in unknown setup step")
		}
	} else {
		fyneUI.mainWindow.SetMainMenu(fyneUI.mainMenu)
		fyneUI.mainWindow.SetContent(fyneUI.mainContainer)
		fyneUI.mainContainer.Show()
	}
}

func (fyneUI *Fyne) buildMainContainer() {
	//
	// Logo and welcome message / instructions to be shown before a thread is selected
	//
	logo := canvas.NewImageFromResource(newEmbeddedResource("assets/logo.png"))
	logo.FillMode = canvas.ImageFillContain
	// TODO: choose reasonable values here
	// https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
	logo.SetMinSize(fyne.NewSize(228, 167))

	fyneUI.defaultContainer = container.NewMax(
		container.New(
			layout.NewCenterLayout(),
			container.NewVBox(
				logo,
				widget.NewLabel("Select a thread on the left to get started"),
			),
		),
	)
	fyneUI.chatContainer = container.NewMax()
	fyneUI.chatContainer.Objects = []fyne.CanvasObject{fyneUI.defaultContainer}

	fyneUI.threadVBox = container.NewVBox()
	threads := container.NewHBox(container.NewVScroll(fyneUI.threadVBox), widget.NewSeparator())

	if fyne.CurrentDevice().IsMobile() {
		icon := canvas.NewImageFromResource(newEmbeddedResource("assets/icon.png"))
		icon.FillMode = canvas.ImageFillContain
		icon.SetMinSize(fyne.NewSize(theme.TextHeadingSize(), theme.TextHeadingSize()))

		threadSearch := widget.NewButtonWithIcon("", theme.SearchIcon(), func() {
			// TODO: replace the top bar with a search entry for n-gram search of thread names, switch back to
			// icon, title, and search icon when entry loses focus
		})
		threadSearch.Importance = widget.LowImportance

		settings := widget.NewButtonWithIcon("", theme.MenuIcon(), func() {
			fyneUI.showMobileMenu()
		})
		settings.Importance = widget.LowImportance

		topButtons := container.NewHBox(
			threadSearch,
			settings,
		)
		logoSearchAndMenu := container.New(
			layout.NewBorderLayout(nil, nil, nil, topButtons),
			topButtons,
			container.NewHBox(
				icon,
				widget.NewRichText(&widget.TextSegment{
					Style: widget.RichTextStyle{
						SizeName: theme.SizeNameHeadingText,
					},
					Text: "Bounce",
				}),
			),
		)

		fyneUI.mainContainer = container.New(
			layout.NewBorderLayout(fyneUI.networkOfflineWarning, nil, nil, nil),
			fyneUI.networkOfflineWarning,
			container.New(
				layout.NewBorderLayout(logoSearchAndMenu, nil, nil, nil),
				logoSearchAndMenu,
				container.NewVScroll(fyneUI.threadVBox),
			),
		)
	} else {
		fyneUI.mainContainer = container.New(
			layout.NewBorderLayout(fyneUI.networkOfflineWarning, nil, threads, nil),
			fyneUI.networkOfflineWarning,
			threads,
			fyneUI.chatContainer,
		)
	}
}

func (fyneUI *Fyne) buildDatabaseLoading() {
	logo := canvas.NewImageFromResource(newEmbeddedResource("assets/logo.png"))
	logo.FillMode = canvas.ImageFillContain
	// TODO: choose reasonable values here
	// https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
	logo.SetMinSize(fyne.NewSize(228, 167))

	fyneUI.databaseLoading = container.NewMax(
		container.New(
			layout.NewCenterLayout(),
			container.NewVBox(
				logo,
				widget.NewLabel("Loading the database..."), // TODO: just make this generic
				widget.NewProgressBarInfinite(),
			),
		),
	)
}

func (fyneUI *Fyne) buildNewInstall() {
	logo := canvas.NewImageFromResource(newEmbeddedResource("assets/logo.png"))
	logo.FillMode = canvas.ImageFillContain
	// TODO: choose reasonable values here
	// https://github.com/fyne-io/fyne/blob/v2.0.3/cmd/fyne_demo/tutorials/welcome.go#L25
	logo.SetMinSize(fyne.NewSize(228, 167))

	header := container.NewVBox(
		container.NewCenter(logo),
		//container.NewCenter(widget.NewLabel("Welcome to Bounce!")),
	)

	// TODO: make these buttons nice custom clickable widgets with images
	selectNewProfile := widget.NewButton("Create a new profile", func() {
		fyneUI.setupStep = setupStepProfile
		fyneUI.mainWindow.SetContent(fyneUI.newProfileCreator)
		fyneUI.newProfileCreator.Show()
	})
	selectSyncDevice := widget.NewButton("Add this device to an existing profile", func() {
		// TODO: display the page to select a file then wait for confirmation, bulid this:
		fyneUI.mainWindow.SetContent(fyneUI.newSyncDevice)
		fyneUI.newSyncDevice.Show()
	})

	content := container.NewGridWithColumns(
		1,
		selectNewProfile,
		selectSyncDevice,
	)

	fyneUI.newInstall = container.New(
		layout.NewBorderLayout(header, nil, nil, nil),
		header,
		content,
	)
}

func (fyneUI *Fyne) buildNewProfileCreator() {
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
	userIcon := newDefaultImage(uuid.Nil, userIconName, 128, func() {
		log.Info("user wants to select the image for a new profile")
	})

	deviceNameEntry := widget.NewEntry()
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
		container.NewCenter(userIcon),
		profileForm,
	)

	saveButton := widget.NewButton("Save", func() {
		if profileNameEntry.Text == "" {
			dialog.ShowError(errors.New("Profile name must be set"), fyneUI.mainWindow)
			return
		}
		if deviceNameEntry.Text == "" {
			dialog.ShowError(errors.New("Device name must be set"), fyneUI.mainWindow)
			return
		}
		id, err := fyneUI.callbacks.SetProfile(profileNameEntry.Text, deviceNameEntry.Text) // TODO: send profile image bytes if set
		if err != nil {
			dialog.ShowError(errors.New("Error saving profile: "+err.Error()), fyneUI.mainWindow)
			return
		}
		profile := makeUser(id, profileNameEntry.Text)
		fyneUI.users.add(profile)
		fyneUI.profile = profile
		fyneUI.showMainContainer()
	})
	saveButton.Importance = widget.HighImportance
	backButton := widget.NewButton("Back", func() {
		fyneUI.setupStep = setupStepInit
		fyneUI.mainWindow.SetContent(fyneUI.newInstall)
		fyneUI.newInstall.Show()
	})
	actionButtons := container.New(
		layout.NewBorderLayout(nil, nil, backButton, saveButton),
		saveButton,
		backButton,
	)

	fyneUI.newProfileCreator = container.New(
		layout.NewBorderLayout(nil, actionButtons, nil, nil),
		actionButtons,
		container.NewMax(profileDetails),
	)
}
