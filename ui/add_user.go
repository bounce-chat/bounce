package ui

import (
	"bytes"
	"errors"
	"image"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/sensor"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/bounce-chat/bounce/chat"
	go_qr "github.com/piglig/go-qr"
	"github.com/rymdport/go-qrcode"
	log "github.com/sirupsen/logrus"
)

var stopAddUserCameraProcessing chan bool
var addUserScanner = make(chan image.Image)

type addUser struct {
	currentStep  *widget.Label
	progressBar  *widget.ProgressBarInfinite
	scanEntry    *widget.Entry
	displayEntry *widget.Entry
	qrCode       *canvas.Image
	scanButton   *widget.Button
	shareButton  *widget.Button
	videoStream  *canvas.Image
}

func (ui *ui) showAddUser() {
	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeAddUser})
	}
	ui.state.currentView = viewTypeAddUser

	ui.widgets.addUser.scanEntry.Text = ""
	ui.widgets.addUser.scanEntry.Refresh()
	ui.widgets.addUser.progressBar.Hide()
	ui.widgets.addUser.currentStep.Text = ""
	ui.widgets.addUser.currentStep.Refresh()
	ui.widgets.addUser.currentStep.Hide()

	newAddUserString := ui.bounce.GetNewAddUserString()
	ui.widgets.addUser.displayEntry.SetText(newAddUserString)
	qrData, err := qrcode.Encode(newAddUserString, qrcode.Medium, 256)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error QR encoding add user string")
	} else {
		qrImg, _, err := image.Decode(bytes.NewReader(qrData))
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error decoding QR data as image")
		} else {
			ui.widgets.addUser.qrCode.Image = qrImg
			ui.widgets.addUser.qrCode.Refresh()
		}
	}

	ui.widgets.addUser.shareButton.Importance = widget.HighImportance
	ui.widgets.addUser.shareButton.Refresh()
	ui.widgets.addUser.scanButton.Importance = widget.MediumImportance
	ui.widgets.addUser.scanButton.Refresh()

	ui.containers.addUserContent.Objects = []fyne.CanvasObject{ui.containers.displayAddUserString}
	ui.containers.addUserContent.Refresh()
	ui.setContent(viewTypeAddUser, ui.views.addUser)
}

func (ui *ui) buildAddUser() {
	ui.buildScanUser()
	ui.buildDisplayAddUserString()

	var closeBar fyne.CanvasObject
	if fyne.CurrentDevice().IsMobile() {
		closeButton := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			}
		})
		closeButton.Importance = widget.LowImportance

		closeBar = container.New(
			layout.NewBorderLayout(nil, nil, closeButton, nil),
			closeButton,
		)
	} else {
		closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack()
			} else {
				ui.showMainContainer()
			}
		})
		closeButton.Importance = widget.LowImportance

		closeBar = container.New(
			layout.NewBorderLayout(nil, nil, nil, closeButton),
			closeButton,
		)
	}

	ui.widgets.addUser.shareButton = widget.NewButton("Share", func() {
		ui.widgets.addUser.shareButton.Importance = widget.HighImportance
		ui.widgets.addUser.shareButton.Refresh()
		ui.widgets.addUser.scanButton.Importance = widget.MediumImportance
		ui.widgets.addUser.scanButton.Refresh()

		ui.containers.addUserContent.Objects = []fyne.CanvasObject{ui.containers.displayAddUserString}
		ui.containers.addUserContent.Refresh()
	})
	ui.widgets.addUser.shareButton.Importance = widget.HighImportance
	ui.widgets.addUser.scanButton = widget.NewButton("Scan", func() {
		cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice)
		if isCameraDevice && !cameraIsRunning {
			cameraDevice.StartPreview()
			cameraIsRunning = true
			cameraRunningFor = viewTypeAddUser
			go ui.processCameraFramesForAddUser()
		}
		ui.widgets.addUser.scanButton.Importance = widget.HighImportance
		ui.widgets.addUser.scanButton.Refresh()
		ui.widgets.addUser.shareButton.Importance = widget.MediumImportance
		ui.widgets.addUser.shareButton.Refresh()

		ui.containers.addUserContent.Objects = []fyne.CanvasObject{ui.containers.scanUser}
		ui.containers.addUserContent.Refresh()
		if !isCameraDevice {
			ui.window.Canvas().Focus(ui.widgets.addUser.scanEntry)
		}
	})
	ui.widgets.addUser.scanButton.Importance = widget.MediumImportance
	header := container.NewVBox(
		closeBar,
		container.NewCenter(container.NewHBox(
			ui.widgets.addUser.shareButton,
			ui.widgets.addUser.scanButton,
		)),
	)
	ui.containers.addUserContent = container.NewStack()
	ui.views.addUser = container.New(
		layout.NewBorderLayout(header, nil, nil, nil),
		header,
		ui.containers.addUserContent,
	)
}

func (ui *ui) buildScanUser() {
	cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice)

	ui.widgets.addUser = &addUser{
		currentStep: widget.NewLabel(""),
		progressBar: widget.NewProgressBarInfinite(),
		scanEntry:   widget.NewEntry(),
	}

	ui.widgets.addUser.scanEntry.OnSubmitted = func(str string) {
		ui.widgets.addUser.progressBar.Start()
		ui.widgets.addUser.progressBar.Show()
		go func() {
			if ui.state.networkState == networkStateStarting || ui.state.networkState == networkStateOffline {
				fyne.Do(func() {
					ui.widgets.addUser.currentStep.Text = "Waiting for network..."
					ui.widgets.addUser.currentStep.Refresh()
					ui.widgets.addUser.currentStep.Show()
				})
				for {
					time.Sleep(500 * time.Millisecond) // TODO: use a callback for this?
					if ui.state.networkState == networkStateOnline {
						break
					}
				}
			}

			fyne.Do(func() {
				ui.widgets.addUser.currentStep.Text = "Sending friend request..."
				ui.widgets.addUser.currentStep.Refresh()
				ui.widgets.addUser.currentStep.Show()
			})
			err := ui.bounce.RequestToAddUser(strings.TrimSpace(str))
			if err == nil {
				fyne.Do(func() {
					ui.widgets.addUser.currentStep.Text = "Waiting for response..."
					ui.widgets.addUser.currentStep.Refresh()
					ui.views.addUser.Refresh()
				})
			} else {
				fyne.Do(func() {
					ui.widgets.addUser.scanEntry.Text = ""
					ui.widgets.addUser.progressBar.Hide()
					ui.widgets.addUser.currentStep.Hide()
					ui.containers.scanUser.Refresh()
					ui.showDialog(dialog.NewError(errors.New("Error sending friend request: "+err.Error()), ui.window), func() {
						if isCameraDevice && !cameraIsRunning {
							cameraDevice.StartPreview()
							go ui.processCameraFramesForAddUser()
							cameraIsRunning = true
							cameraRunningFor = viewTypeAddUser
						}
					})
				})
			}
		}()
	}

	if isCameraDevice {
		size := float32(300)

		ui.widgets.addUser.videoStream = &canvas.Image{
			FillMode: canvas.ImageFillCover,
		}
		ui.widgets.addUser.videoStream.Resize(fyne.Size{Height: size, Width: size})
		ui.widgets.addUser.videoStream.SetMinSize(fyne.Size{Height: size, Width: size})

		guideImage, _ := assets.ReadFile("assets/qr-guide.png")
		guide := canvas.NewImageFromReader(bytes.NewReader(guideImage), "qr-guide.png")
		guide.Translucency = 0.5
		guide.Resize(fyne.Size{Height: size, Width: size})
		guide.SetMinSize(fyne.Size{Height: size, Width: size})

		var haveResult atomic.Bool
		go func() {
			for frame := range addUserScanner {
				text, err := go_qr.Decode(frame)
				if err == nil {
					fyne.Do(func() {
						if haveResult.Load() {
							return
						}
						haveResult.Store(true)
						if cameraIsRunning {
							cameraDevice.StopPreview()
							cameraIsRunning = false
							select {
							case stopAddUserCameraProcessing <- true:
							default:
							}
						}
						ui.widgets.addUser.scanEntry.SetText(text)
						ui.widgets.addUser.scanEntry.OnSubmitted(text)
						haveResult.Store(false)
					})
				}
			}
		}()

		ui.containers.scanUser = container.NewVBox(
			widget.NewLabel("Scan their QR code, or paste in the string to add friend"),
			container.NewCenter(container.NewStack(
				ui.widgets.addUser.videoStream,
				guide,
			)),
			ui.widgets.addUser.scanEntry,
			ui.widgets.addUser.currentStep,
			ui.widgets.addUser.progressBar,
		)
	} else {
		ui.containers.scanUser = container.NewVBox(
			widget.NewLabel("Paste in the string to add friend"),
			ui.widgets.addUser.scanEntry,
			ui.widgets.addUser.currentStep,
			ui.widgets.addUser.progressBar,
		)
	}
}

func (ui *ui) buildDisplayAddUserString() {
	title := widget.NewLabel("Scan or paste this on your friend's device:")

	ui.widgets.addUser.qrCode = &canvas.Image{
		FillMode: canvas.ImageFillContain,
	}
	ui.widgets.addUser.qrCode.SetMinSize(fyne.Size{Height: 300, Width: 300})

	ui.widgets.addUser.displayEntry = widget.NewEntry()
	ui.widgets.addUser.displayEntry.ActionItem = widget.NewButtonWithIcon("", theme.ContentCopyIcon(), func() { ui.window.Clipboard().SetContent(ui.widgets.addUser.displayEntry.Text) })

	ui.containers.displayAddUserString = container.New(
		layout.NewBorderLayout(title, nil, nil, nil),
		title,
		container.NewVBox(
			ui.widgets.addUser.qrCode,
			ui.widgets.addUser.displayEntry,
		),
	)
}

func (ui *ui) UserAdded(u chat.User) {
	fyne.DoAndWait(func() {
		ui.widgets.addUser.currentStep.Text = "Success!"
		ui.widgets.addUser.currentStep.Refresh()
		ui.widgets.addUser.progressBar.Stop()
		ui.widgets.addUser.progressBar.Hide()
		newUser := makeUser(u)
		ui.users.add(newUser)
		if ui.state.currentView == viewTypeAddUser {
			if fyne.CurrentDevice().IsMobile() {
				ui.mobileBack() // Exit to menu
				ui.mobileBack() // Exit menu to home
			} else {
				ui.showMainContainer()
			}
		}
		if !ui.state.initialSyncIncomplete {
			ui.NewDirectMessage(u) // TODO: no DM state exists for the user yet
			ui.showDialog(dialog.NewInformation("New contact added", u.Name+" was added as a friend", ui.window), nil)
		}
	})
}

func (ui *ui) AddUserRequestRejected(peer string) {
	fyne.DoAndWait(func() {
		ui.showDialog(dialog.NewError(errors.New("Friend request rejected, make sure you scan the device quickly"), ui.window), nil)
	})
}

func (ui *ui) processCameraFramesForAddUser() {
	cameraDevice, isCameraDevice := fyne.CurrentDevice().(sensor.CameraDevice)
	if !isCameraDevice {
		return
	}
	preview := cameraDevice.Preview()

	for {
		select {
		case img := <-preview:
			fyne.Do(func() {
				if ui.widgets.addUser.videoStream == nil {
					return
				}
				ui.widgets.addUser.videoStream.Image = img
				ui.widgets.addUser.videoStream.Refresh()
			})
			select {
			case addUserScanner <- img:
			default:
			}
		case <-stopAddUserCameraProcessing:
			return
		}
	}
}
