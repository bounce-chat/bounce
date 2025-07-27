package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

type newDM struct {
	searchEntry *widget.Entry
	scroll      *container.Scroll
}

func (ui *ui) showNewDM() {
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeNewDM})
	}
	ui.state.currentView = viewTypeNewDM
	ui.widgets.newDM.searchEntry.Text = ""
	ui.widgets.newDM.searchEntry.Refresh()
	ui.refreshAllUsersDMLinks()
	ui.window.SetContent(ui.views.newDM)
	ui.window.Canvas().Focus(ui.widgets.newDM.searchEntry)
}

func (ui *ui) buildNewDM() {
	ui.widgets.newDM = &newDM{
		scroll: container.NewVScroll(container.NewVBox()),
	}

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
		}
	})
	closeButton.Importance = widget.LowImportance

	closeBar := container.New(
		layout.NewBorderLayout(nil, nil, nil, closeButton),
		closeButton,
	)

	ui.widgets.newDM.searchEntry = widget.NewEntry()
	ui.widgets.newDM.searchEntry.OnChanged = func(str string) {
		ui.refreshAllUsersDMLinks()
	}
	ui.views.newDM = container.New(
		layout.NewBorderLayout(closeBar, nil, nil, nil),
		closeBar,
		container.New(
			layout.NewBorderLayout(ui.widgets.newDM.searchEntry, nil, nil, nil),
			ui.widgets.newDM.searchEntry,
			ui.widgets.newDM.scroll,
		),
	)
}

func (ui *ui) refreshAllUsersDMLinks() {
	usersBox := container.NewVBox()
	for _, thisUser := range ui.users.search(ui.widgets.newDM.searchEntry.Text) {
		func(u *user) {
			openDMButton := newUserButton(
				newDefaultImage(u.id, u.images, u.initials, theme.IconInlineSize()*2, ui.bounce.GetFileData, nil),
				u.getDisplayName(),
				false,
				func() {
					dm, ok := ui.threads.getDM(u.id)
					if !ok {
						defer ui.bounce.SetOpenDM(u.id, true)
						dm = ui.openAndPopulateDM(u)
						if dm == nil {
							return
						}
					}

					if fyne.CurrentDevice().IsMobile() {
						ui.mobileBack() // Close new DM view
						ui.mobileBack() // Close menu
						ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeThread, context: dm.user.id})
						ui.state.currentView = viewTypeThread
					} else {
						ui.showMainContainer()
					}
					ui.bounce.UserConnectionDesired(u.id)
					ui.displayThread(dm)
				},
			)
			usersBox.Objects = append(
				usersBox.Objects,
				openDMButton,
			)
		}(thisUser)
	}
	ui.widgets.newDM.scroll.Content = usersBox
	ui.widgets.newDM.scroll.Refresh()
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
