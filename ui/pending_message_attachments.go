package ui

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/bbrks/go-blurhash"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	log "github.com/sirupsen/logrus"
	"golang.org/x/image/draw"
)

type messageAttachment struct {
	widget.BaseWidget

	id       uuid.UUID
	reader   fyne.URIReadCloser
	fileSize int64
	isImage  bool
	blurHash string
	width    int
	height   int

	icon     *canvas.Image
	filename *widget.RichText
	size     *canvas.Text
	action   *widget.Button
	progress *widget.ProgressBar
}

func newMessageAttachment(id uuid.UUID, name string, size int64) *messageAttachment {
	ma := &messageAttachment{
		id:       id,
		reader:   nil,
		fileSize: size,
		isImage:  false,
		icon:     canvas.NewImageFromResource(theme.FileIcon()),
		filename: widget.NewRichTextWithText(name),
		size: &canvas.Text{
			Text:     fileSizeString(size),
			TextSize: theme.TextSize() * 0.75,
			TextStyle: fyne.TextStyle{
				Italic: true,
			},
		},
		action:   widget.NewButtonWithIcon("", theme.DownloadIcon(), nil),
		progress: widget.NewProgressBar(),
	}
	ma.filename.Truncation = fyne.TextTruncateEllipsis
	ma.action.Importance = widget.LowImportance
	ma.icon.FillMode = canvas.ImageFillContain
	ma.progress.Hide()
	ma.progress.TextFormatter = func() string { return "" }

	ma.ExtendBaseWidget(ma)

	return ma
}

func newPendingMessageAttachment(id uuid.UUID, reader fyne.URIReadCloser, actionCallback func()) (*messageAttachment, error) {
	var size int64
	var err error
	size, reader, err = fileSizeInReader(reader)
	if err != nil {
		log.WithFields(log.Fields{
			"id":    id,
			"error": err.Error(),
		}).Error("error getting file size")
		return nil, err
	}
	sizeString := fileSizeString(size)

	var icon *canvas.Image
	isImage := false
	blurHash := ""
	width := 0
	height := 0
	if size < chat.EmbeddedFileLimit {
		var err error
		imageBytes := []byte{}
		if fyne.CurrentDevice().IsMobile() {
			imageBytes, err = io.ReadAll(reader)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error reading all from reader")
			}
			reader.Close()
			reader, err = storage.Reader(reader.URI())
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error re-opening reader")
			}
		} else {
			diskReader, err := os.Open(reader.URI().Path())
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
					"path":  reader.URI().Path(),
				}).Error("file selected as message attachment cannot be read from disk")
				return nil, err
			}
			imageBytes, err = io.ReadAll(diskReader)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error reading all from disk")
				return nil, err
			}
			diskReader.Close()
		}

		img, _, err := image.Decode(bytes.NewReader(imageBytes))
		if err == nil {
			icon = canvas.NewImageFromImage(img)
			isImage = true

			// Scale the image down to make BlurHash faster
			larger := img.Bounds().Dx()
			if img.Bounds().Dy() > larger {
				larger = img.Bounds().Dy()
			}
			scaleFactor := 1
			if larger > 32 {
				scaleFactor = larger / 32
			}
			smaller := image.NewRGBA(image.Rect(0, 0, img.Bounds().Max.X/scaleFactor, img.Bounds().Max.Y/scaleFactor))
			draw.NearestNeighbor.Scale(smaller, smaller.Rect, img, img.Bounds(), draw.Over, nil)

			blur, err := blurhash.Encode(4, 4, smaller)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Error("error generating blur hash for pending message attachment")
			} else {
				blurHash = blur
			}
			width = img.Bounds().Dx()
			height = img.Bounds().Dy()
		} else {
			icon = canvas.NewImageFromResource(theme.FileIcon())
		}
	} else {
		icon = canvas.NewImageFromResource(theme.FileIcon())
	}

	ma := &messageAttachment{
		id:       id,
		reader:   reader,
		fileSize: size,
		isImage:  isImage,
		blurHash: blurHash,
		width:    width,
		height:   height,
		icon:     icon,
		filename: widget.NewRichTextWithText(reader.URI().Name()),
		size: &canvas.Text{
			Text:     sizeString,
			TextSize: theme.TextSize() * 0.75,
			TextStyle: fyne.TextStyle{
				Italic: true,
			},
		},
		action:   widget.NewButtonWithIcon("", theme.CancelIcon(), actionCallback),
		progress: widget.NewProgressBar(),
	}
	ma.filename.Truncation = fyne.TextTruncateEllipsis
	ma.action.Importance = widget.LowImportance
	ma.icon.FillMode = canvas.ImageFillContain
	ma.progress.Hide()
	ma.progress.TextFormatter = func() string { return "" }

	ma.ExtendBaseWidget(ma)

	return ma, nil
}

func (ma *messageAttachment) CreateRenderer() fyne.WidgetRenderer {
	ma.ExtendBaseWidget(ma)

	mar := &messageAttachmentRenderer{
		ma: ma,
	}

	return mar
}

func (ma *messageAttachment) idealWidth() float32 {
	return theme.Padding() +
		ma.icon.MinSize().Width +
		theme.Padding() +
		fyne.MeasureText(
			ma.filename.Segments[0].(*widget.TextSegment).Text,
			theme.Size(ma.filename.Segments[0].(*widget.TextSegment).Style.SizeName),
			ma.filename.Segments[0].(*widget.TextSegment).Style.TextStyle,
		).Width +
		theme.Padding() +
		fyne.MeasureText(
			ma.size.Text,
			ma.size.TextSize,
			ma.size.TextStyle,
		).Width +
		theme.Padding()*7 +
		ma.action.MinSize().Width +
		theme.Padding()
}

type messageAttachmentRenderer struct {
	ma *messageAttachment
}

func (mar *messageAttachmentRenderer) Destroy() {}

func (mar *messageAttachmentRenderer) Layout(size fyne.Size) {
	mar.ma.icon.Resize(fyne.Size{
		theme.IconInlineSize(),
		theme.IconInlineSize(),
	})
	mar.ma.icon.Move(fyne.Position{
		theme.Padding(),
		(size.Height - mar.ma.icon.Size().Height) / 2,
	})

	filenameSize := size
	filenameSize.Width -= theme.Padding()*5 + mar.ma.action.MinSize().Width + mar.ma.size.MinSize().Width + mar.ma.icon.Size().Width
	mar.ma.filename.Resize(filenameSize)
	mar.ma.filename.Move(fyne.Position{
		theme.Padding()*2 + mar.ma.icon.Size().Width,
		(size.Height - mar.ma.filename.MinSize().Height) / 2,
	})

	mar.ma.size.Resize(mar.ma.size.MinSize())
	mar.ma.size.Move(fyne.Position{
		size.Width - mar.ma.action.MinSize().Width - mar.ma.size.MinSize().Width - theme.Padding()*2,
		(size.Height - mar.ma.size.MinSize().Height) / 2,
	})

	mar.ma.action.Resize(mar.ma.action.MinSize())
	mar.ma.action.Move(fyne.Position{size.Width - mar.ma.action.MinSize().Width - theme.Padding()*2, 0})

	if mar.ma.progress.Visible() {
		mar.ma.progress.Resize(fyne.Size{Height: size.Height, Width: size.Width - theme.Padding()*3})
		mar.ma.progress.Move(fyne.Position{})
	}
}

func (mar *messageAttachmentRenderer) MinSize() fyne.Size {
	size := mar.ma.filename.MinSize()
	size.Width += theme.Padding()*5 + mar.ma.icon.MinSize().Width + mar.ma.action.MinSize().Width + mar.ma.size.MinSize().Width
	return size
}

func (mar *messageAttachmentRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		mar.ma.progress,
		mar.ma.icon,
		mar.ma.filename,
		mar.ma.size,
		mar.ma.action,
	}
}

func (mar *messageAttachmentRenderer) Refresh() {
	for _, obj := range mar.Objects() {
		obj.Refresh()
	}
}

type pendingMessageAttachments struct {
	widget.BaseWidget

	files []*messageAttachment

	attachments *fyne.Container
	scroll      *container.Scroll
}

func newPendingMessageAttachments() *pendingMessageAttachments {
	attachments := container.NewVBox()
	scroll := container.NewScroll(attachments)

	pmas := &pendingMessageAttachments{
		attachments: attachments,
		scroll:      scroll,
	}
	pmas.ExtendBaseWidget(pmas)

	return pmas
}

func (pmas *pendingMessageAttachments) add(reader fyne.URIReadCloser) {
	id := uuid.New()
	newFile, err := newPendingMessageAttachment(id, reader, func() { pmas.remove(id) })
	if err == nil {
		pmas.files = append(pmas.files, newFile)
		pmas.Refresh()
	}
}

func (pmas *pendingMessageAttachments) remove(id uuid.UUID) {
	pruned := []*messageAttachment{}

	for _, file := range pmas.files {
		if file.id != id {
			pruned = append(pruned, file)
		}
	}
	pmas.files = pruned
	pmas.Refresh()
}

func (pmas *pendingMessageAttachments) extract() []*messageAttachment {
	content := pmas.files
	pmas.files = []*messageAttachment{}
	pmas.Refresh()

	return content
}

func (pmas *pendingMessageAttachments) CreateRenderer() fyne.WidgetRenderer {
	pmas.ExtendBaseWidget(pmas)

	pmasr := &pendingMessageAttachmentsRenderer{
		pmas: pmas,
	}

	return pmasr
}

type pendingMessageAttachmentsRenderer struct {
	pmas *pendingMessageAttachments
}

func (pmasr *pendingMessageAttachmentsRenderer) Destroy() {}

func (pmasr *pendingMessageAttachmentsRenderer) Layout(size fyne.Size) {
	pmasr.pmas.scroll.Resize(size)
	pmasr.pmas.scroll.Move(fyne.Position{})
}

func (pmasr *pendingMessageAttachmentsRenderer) MinSize() fyne.Size {
	size := fyne.Size{}

	for i, obj := range pmasr.pmas.files {
		if i > 3 {
			break
		}
		size.Height += obj.MinSize().Height
	}
	size.Height += theme.Padding() * float32(len(pmasr.pmas.files)+1)
	return size
}

func (pmasr *pendingMessageAttachmentsRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		pmasr.pmas.scroll,
	}
}

func (pmasr *pendingMessageAttachmentsRenderer) Refresh() {
	pmasr.pmas.attachments.Objects = []fyne.CanvasObject{}
	for _, file := range pmasr.pmas.files {
		pmasr.pmas.attachments.Add(file)
	}

	if len(pmasr.pmas.files) == 0 {
		pmasr.pmas.Hide()
	} else {
		pmasr.pmas.Show()
	}

	for _, obj := range pmasr.Objects() {
		obj.Refresh()
	}
}

func fileSizeString(size int64) string {
	if size < 0 {
		return ""
	}

	unit := int64(1000)
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}

	div := unit
	exp := 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	suffixes := []string{"KB", "MB", "GB", "TB", "PB", "EB"}
	if exp > len(suffixes) {
		return ""
	}

	return fmt.Sprintf("%.1f %s", float64(size)/float64(div), suffixes[exp])
}

func fileSizeInReader(reader fyne.URIReadCloser) (int64, fyne.URIReadCloser, error) {
	size := int64(0)
	if fyne.CurrentDevice().IsMobile() {
		var err error
		size, err = io.Copy(io.Discard, reader)
		if err != nil {
			return 0, nil, err
		}
		reader.Close()
		reader, err = storage.Reader(reader.URI())
		if err != nil {
			return 0, nil, err
		}
	} else {
		f, err := os.Stat(reader.URI().Path())
		if err != nil {
			return 0, nil, err
		}
		size = f.Size()
	}

	return size, reader, nil
}
