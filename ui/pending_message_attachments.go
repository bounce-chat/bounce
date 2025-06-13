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

type pendingMessageAttachment struct {
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
	remove   *widget.Button
}

func newPendingMessageAttachment(id uuid.UUID, reader fyne.URIReadCloser, removeCallback func()) (*pendingMessageAttachment, error) {
	size := int64(0)
	if fyne.CurrentDevice().IsMobile() {
		var err error
		size, err = io.Copy(io.Discard, reader)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error reading data to get size")
		}
		reader.Close()
		reader, err = storage.Reader(reader.URI())
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error re-opening reader")
		}
	} else {
		f, err := os.Stat(reader.URI().Path())
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"path":  reader.URI().Path(),
			}).Error("file selected as message attachment cannot be read from disk")
			return nil, err
		}
		size = f.Size()
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

	pma := &pendingMessageAttachment{
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
		remove: widget.NewButtonWithIcon("", theme.CancelIcon(), removeCallback),
	}
	pma.filename.Truncation = fyne.TextTruncateEllipsis
	pma.remove.Importance = widget.LowImportance
	pma.icon.FillMode = canvas.ImageFillContain

	pma.ExtendBaseWidget(pma)

	return pma, nil
}

func (pma *pendingMessageAttachment) CreateRenderer() fyne.WidgetRenderer {
	pma.ExtendBaseWidget(pma)

	pmar := &pendingMessageAttachmentRenderer{
		pma: pma,
	}

	return pmar
}

type pendingMessageAttachmentRenderer struct {
	pma *pendingMessageAttachment
}

func (pmar *pendingMessageAttachmentRenderer) Destroy() {}

func (pmar *pendingMessageAttachmentRenderer) Layout(size fyne.Size) {
	pmar.pma.icon.Resize(fyne.Size{
		theme.IconInlineSize(),
		theme.IconInlineSize(),
	})
	pmar.pma.icon.Move(fyne.Position{
		theme.Padding(),
		(size.Height - pmar.pma.icon.Size().Height) / 2,
	})

	filenameSize := size
	filenameSize.Width -= theme.Padding()*5 + pmar.pma.remove.MinSize().Width + pmar.pma.size.MinSize().Width + pmar.pma.icon.Size().Width
	pmar.pma.filename.Resize(filenameSize)
	pmar.pma.filename.Move(fyne.Position{
		theme.Padding()*2 + pmar.pma.icon.Size().Width,
		(size.Height - pmar.pma.filename.MinSize().Height) / 2,
	})

	pmar.pma.size.Resize(pmar.pma.size.MinSize())
	pmar.pma.size.Move(fyne.Position{
		size.Width - pmar.pma.remove.MinSize().Width - pmar.pma.size.MinSize().Width - theme.Padding()*2,
		(size.Height - pmar.pma.size.MinSize().Height) / 2,
	})

	pmar.pma.remove.Resize(pmar.pma.remove.MinSize())
	pmar.pma.remove.Move(fyne.Position{size.Width - pmar.pma.remove.MinSize().Width - theme.Padding(), theme.Padding()})
}

func (pmar *pendingMessageAttachmentRenderer) MinSize() fyne.Size {
	size := pmar.pma.filename.MinSize()
	size.Width += theme.Padding()*5 + pmar.pma.icon.MinSize().Width + pmar.pma.remove.MinSize().Width + pmar.pma.size.MinSize().Width
	return size
}

func (pmar *pendingMessageAttachmentRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		pmar.pma.icon,
		pmar.pma.filename,
		pmar.pma.size,
		pmar.pma.remove,
	}
}

func (pmar *pendingMessageAttachmentRenderer) Refresh() {
	for _, obj := range pmar.Objects() {
		obj.Refresh()
	}
}

type pendingMessageAttachments struct {
	widget.BaseWidget

	files []*pendingMessageAttachment

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
	pruned := []*pendingMessageAttachment{}

	for _, file := range pmas.files {
		if file.id != id {
			pruned = append(pruned, file)
		}
	}
	pmas.files = pruned
	pmas.Refresh()
}

func (pmas *pendingMessageAttachments) extract() []*pendingMessageAttachment {
	content := pmas.files
	pmas.files = []*pendingMessageAttachment{}
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
