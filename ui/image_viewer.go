package ui

import (
	"bytes"
	"image"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/bbrks/go-blurhash"
	"github.com/bounce-chat/bounce/chat"
	log "github.com/sirupsen/logrus"
)

// The width the blurhash of an attachment is decoded at while the viewer reads and decodes the file itself
var imageViewerPlaceholderWidth = float32(32)

// The image viewer shows the image attachments of one message at full size, one at a time.  It is handed the
// attachments rather than their pixels: the selected one is read and decoded when it is shown and dropped when
// the next is, so a full size decode lives only while it is on screen.
type imageViewer struct {
	viewer     *fyne.Container
	imageArea  *fyne.Container
	download   *widget.Button
	left       *widget.Button
	right      *widget.Button
	images     []*chat.ImageAttachment
	index      int
	generation int // advanced each time the shown image changes, so a slow load of the previous one is discarded
}

func (ui *ui) showImageViewer(images []*chat.ImageAttachment, index int) {
	if index > len(images)-1 {
		log.WithFields(log.Fields{
			"index":  index,
			"images": len(images),
		}).Error("cannot display image viewer with index beyond number of images")
		return
	}

	ui.widgets.imageViewer.images = images
	ui.widgets.imageViewer.index = index

	ui.refreshImageViewer()

	if fyne.CurrentDevice().IsMobile() {
		ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeImageViewer})
	}
	ui.setContent(viewTypeImageViewer, ui.widgets.imageViewer.viewer)
	ui.widgets.imageViewer.viewer.Show()
}

func (v *imageViewer) current() (*chat.ImageAttachment, bool) {
	if v.index < 0 || v.index > len(v.images)-1 {
		log.WithFields(log.Fields{
			"index":  v.index,
			"images": len(v.images),
		}).Error("image viewer index is beyond its images")
		return nil, false
	}
	return v.images[v.index], true
}

func (v *imageViewer) show(img image.Image) {
	shown := canvas.NewImageFromImage(img)
	shown.FillMode = canvas.ImageFillContain
	v.imageArea.Objects = []fyne.CanvasObject{shown}
	v.imageArea.Refresh()
}

func (ui *ui) buildImageViewer() {
	download := widget.NewButtonWithIcon("", theme.DownloadIcon(), func() {
		attachment, ok := ui.widgets.imageViewer.current()
		if !ok {
			return
		}
		data, err := ui.bounce.GetFileData(attachment.ID)
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"file_id": attachment.ID,
			}).Warn("cannot save an image that has not been downloaded")
			return
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
		d.SetFileName(attachment.Name)
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

// refreshImageViewer shows the selected attachment.  Its blurhash goes up at once and the file is read and decoded
// off the UI thread, since a large photo can take a moment; the viewer is one widget whose fields change only on
// the UI thread, so the generation check is all that is needed to drop a load the user has already moved past.
func (ui *ui) refreshImageViewer() {
	v := ui.widgets.imageViewer
	attachment, ok := v.current()
	if !ok {
		return
	}
	v.generation++
	generation := v.generation

	placeholderHeight := scaledHeight(imageViewerPlaceholderWidth, attachment.Width, attachment.Height)
	if placeholder, err := blurhash.Decode(attachment.BlurHash, int(imageViewerPlaceholderWidth), placeholderHeight, 1); err == nil {
		v.show(placeholder)
	} else {
		v.imageArea.Objects = []fyne.CanvasObject{}
		v.imageArea.Refresh()
	}
	v.download.Disable()

	if v.index == 0 {
		v.left.Disable()
	} else {
		v.left.Enable()
	}
	if v.index == len(v.images)-1 {
		v.right.Disable()
	} else {
		v.right.Enable()
	}

	go func() {
		data, err := ui.bounce.GetFileData(attachment.ID)
		if err != nil {
			// Not downloaded yet, so the blurhash stays
			return
		}
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			log.WithFields(log.Fields{
				"error":   err.Error(),
				"file_id": attachment.ID,
			}).Warn("invalid image data on image attachment")
			return
		}
		fyne.Do(func() {
			if v.generation != generation {
				return
			}
			v.show(decoded)
			v.download.Enable()
		})
	}()
}
