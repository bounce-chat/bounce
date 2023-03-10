package ui

import (
	"errors"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
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

	fyneUI.chatContainer = container.NewMax(
		container.New(
			layout.NewCenterLayout(),
			container.NewVBox(
				logo,
				widget.NewLabel("Select a thread on the left to get started"),
			),
		),
	)

	fyneUI.threadVBox = container.NewVBox()
	threads := container.NewHBox(container.NewVScroll(fyneUI.threadVBox), widget.NewSeparator())

	fyneUI.mainContainer = container.New(
		layout.NewBorderLayout(fyneUI.networkOfflineWarning, nil, threads, nil),
		fyneUI.networkOfflineWarning,
		threads,
		fyneUI.chatContainer,
	)
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
	userIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/not_found.png")) // TODO: click to change this
	userIcon.FillMode = canvas.ImageFillContain
	userIcon.SetMinSize(fyne.NewSize(64, 64))

	profileNameEntry := widget.NewEntry()
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
		userIcon,
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
		profile := &user{
			id:   id,
			name: profileNameEntry.Text,
		}
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
