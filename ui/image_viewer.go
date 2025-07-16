package ui

import (
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
)

type imageViewer struct {
	viewer    *fyne.Container
	imageArea *fyne.Container
	download  *widget.Button
	left      *widget.Button
	right     *widget.Button
	images    []image.Image
	data      [][]byte
	index     int
}

func (ui *ui) showImageViewer(images []image.Image, data [][]byte, index int) {
	if index > len(images)-1 {
		log.WithFields(log.Fields{
			"index":  index,
			"images": len(images),
		}).Error("cannot display image viewer with index beyond number of images")
		return
	}

	ui.imageViewer.images = images
	ui.imageViewer.data = data
	ui.imageViewer.index = index

	ui.refreshImageViewer()

	if fyne.CurrentDevice().IsMobile() {
		ui.viewStack = append(ui.viewStack, view{viewType: viewTypeImageViewer})
	}
	ui.currentView = viewTypeImageViewer
	ui.mainWindow.SetContent(ui.imageViewer.viewer)
	ui.imageViewer.viewer.Show()
}

func (ui *ui) buildImageViewer() {
	download := widget.NewButtonWithIcon("", theme.DownloadIcon(), func() {
		if ui.imageViewer.index > len(ui.imageViewer.data)-1 {
			log.WithFields(log.Fields{
				"index":       ui.imageViewer.index,
				"data_length": len(ui.imageViewer.images),
			}).Error("cannot save image data with invalid index")
			return
		}
		data := ui.imageViewer.data[ui.imageViewer.index]
		dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
			if err != nil {
				return
			}

			if writer == nil {
				return
			}

			writer.Write(data)
			writer.Close()
		}, ui.mainWindow).Show()
	})
	download.Importance = widget.LowImportance

	closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
		if fyne.CurrentDevice().IsMobile() {
			ui.mobileBack()
		} else {
			ui.showMainContainer()
		}
	})
	closeButton.Importance = widget.LowImportance

	buttons := container.NewHBox(download, closeButton)
	downloadAndClose := container.New(
		layout.NewBorderLayout(nil, nil, nil, buttons),
		buttons,
	)

	left := widget.NewButtonWithIcon("", theme.NavigateBackIcon(), func() {
		if ui.imageViewer.index > 0 {
			ui.imageViewer.index -= 1
			ui.refreshImageViewer()
		}
	})
	left.Importance = widget.LowImportance
	right := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		if ui.imageViewer.index < len(ui.imageViewer.images)-1 {
			ui.imageViewer.index += 1
			ui.refreshImageViewer()
		}
	})
	right.Importance = widget.LowImportance

	ui.imageViewer = &imageViewer{
		download: download,
		left:     left,
		right:    right,
	}
	ui.imageViewer.imageArea = container.NewStack()
	ui.imageViewer.viewer = container.New(
		layout.NewBorderLayout(downloadAndClose, nil, left, right),
		downloadAndClose,
		left,
		right,
		ui.imageViewer.imageArea,
	)

}

func (ui *ui) refreshImageViewer() {
	if ui.imageViewer.index > len(ui.imageViewer.images)-1 {
		log.WithFields(log.Fields{
			"index":  ui.imageViewer.index,
			"images": len(ui.imageViewer.images),
		}).Error("cannot refresh the image viewer with invalid index")
		return
	}
	image := canvas.NewImageFromImage(ui.imageViewer.images[ui.imageViewer.index])
	image.FillMode = canvas.ImageFillContain
	ui.imageViewer.imageArea.Objects = []fyne.CanvasObject{image}
	ui.imageViewer.imageArea.Refresh()

	if ui.imageViewer.index == 0 {
		ui.imageViewer.left.Disable()
	} else {
		ui.imageViewer.left.Enable()
	}

	if ui.imageViewer.index == len(ui.imageViewer.images)-1 {
		ui.imageViewer.right.Disable()
	} else {
		ui.imageViewer.right.Enable()
	}

	if ui.imageViewer.index > len(ui.imageViewer.data)-1 {
		log.WithFields(log.Fields{
			"index":       ui.imageViewer.index,
			"data_length": len(ui.imageViewer.images),
		}).Error("cannot refresh the image viewer with invalid index")
		return
	}
	data := ui.imageViewer.data[ui.imageViewer.index]
	if len(data) == 0 {
		ui.imageViewer.download.Disable()
	} else {
		ui.imageViewer.download.Enable()
	}
}
