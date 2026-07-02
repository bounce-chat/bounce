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
	names     []string
	index     int
}

func (ui *ui) showImageViewer(images []image.Image, data [][]byte, names []string, index int) {
	if index > len(images)-1 {
		log.WithFields(log.Fields{
			"index":  index,
			"images": len(images),
		}).Error("cannot display image viewer with index beyond number of images")
		return
	}

	ui.widgets.imageViewer.images = images
	ui.widgets.imageViewer.data = data
	ui.widgets.imageViewer.names = names
	ui.widgets.imageViewer.index = index

	ui.refreshImageViewer()

	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeImageViewer})
	}
	ui.state.currentView = viewTypeImageViewer
	ui.window.SetContent(ui.widgets.imageViewer.viewer)
	ui.widgets.imageViewer.viewer.Show()
}

func (ui *ui) buildImageViewer() {
	download := widget.NewButtonWithIcon("", theme.DownloadIcon(), func() {
		if ui.widgets.imageViewer.index > len(ui.widgets.imageViewer.data)-1 {
			log.WithFields(log.Fields{
				"index":       ui.widgets.imageViewer.index,
				"data_length": len(ui.widgets.imageViewer.images),
			}).Error("cannot save image data with invalid index")
			return
		}
		data := ui.widgets.imageViewer.data[ui.widgets.imageViewer.index]

		name := ""
		if ui.widgets.imageViewer.index <= len(ui.widgets.imageViewer.names)-1 {
			name = ui.widgets.imageViewer.names[ui.widgets.imageViewer.index]
		}

		d := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
			if err != nil {
				return
			}

			if writer == nil {
				return
			}

			writer.Write(data)
			writer.Close()
		}, ui.window)
		d.SetFileName(name)
		d.Show()
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
		if ui.widgets.imageViewer.index > 0 {
			ui.widgets.imageViewer.index -= 1
			ui.refreshImageViewer()
		}
	})
	left.Importance = widget.LowImportance
	right := widget.NewButtonWithIcon("", theme.NavigateNextIcon(), func() {
		if ui.widgets.imageViewer.index < len(ui.widgets.imageViewer.images)-1 {
			ui.widgets.imageViewer.index += 1
			ui.refreshImageViewer()
		}
	})
	right.Importance = widget.LowImportance

	ui.widgets.imageViewer = &imageViewer{
		download: download,
		left:     left,
		right:    right,
	}
	ui.widgets.imageViewer.imageArea = container.NewStack()
	ui.widgets.imageViewer.viewer = container.New(
		layout.NewBorderLayout(downloadAndClose, nil, left, right),
		downloadAndClose,
		left,
		right,
		ui.widgets.imageViewer.imageArea,
	)

}

func (ui *ui) refreshImageViewer() {
	if ui.widgets.imageViewer.index > len(ui.widgets.imageViewer.images)-1 {
		log.WithFields(log.Fields{
			"index":  ui.widgets.imageViewer.index,
			"images": len(ui.widgets.imageViewer.images),
		}).Error("cannot refresh the image viewer with invalid index")
		return
	}
	image := canvas.NewImageFromImage(ui.widgets.imageViewer.images[ui.widgets.imageViewer.index])
	image.FillMode = canvas.ImageFillContain
	ui.widgets.imageViewer.imageArea.Objects = []fyne.CanvasObject{image}
	ui.widgets.imageViewer.imageArea.Refresh()

	if ui.widgets.imageViewer.index == 0 {
		ui.widgets.imageViewer.left.Disable()
	} else {
		ui.widgets.imageViewer.left.Enable()
	}

	if ui.widgets.imageViewer.index == len(ui.widgets.imageViewer.images)-1 {
		ui.widgets.imageViewer.right.Disable()
	} else {
		ui.widgets.imageViewer.right.Enable()
	}

	if ui.widgets.imageViewer.index > len(ui.widgets.imageViewer.data)-1 {
		log.WithFields(log.Fields{
			"index":       ui.widgets.imageViewer.index,
			"data_length": len(ui.widgets.imageViewer.images),
		}).Error("cannot refresh the image viewer with invalid index")
		return
	}
	data := ui.widgets.imageViewer.data[ui.widgets.imageViewer.index]
	if len(data) == 0 {
		ui.widgets.imageViewer.download.Disable()
	} else {
		ui.widgets.imageViewer.download.Enable()
	}
}
