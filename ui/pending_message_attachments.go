package ui

import (
	"fmt"
	"image"
	"os"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
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

func newPendingMessageAttachment(id uuid.UUID, reader fyne.URIReadCloser, removeCallback func()) *pendingMessageAttachment {
	f, err := os.Stat(reader.URI().Path())
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"path":  reader.URI().Path(),
		}).Error("file selected as message attachment cannot be read from disk")
	}
	sizeString := fileSizeString(f.Size())

	var icon *canvas.Image
	isImage := false
	blurHash := ""
	width := 0
	height := 0
	if f.Size() < chat.EmbeddedFileLimit {
		diskReader, err := os.Open(reader.URI().Path())
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
				"path":  reader.URI().Path(),
			}).Error("file selected as message attachment cannot be read from disk")
		}
		img, _, err := image.Decode(diskReader)
		if err == nil {
			icon = canvas.NewImageFromImage(img)
			isImage = true

			// Scale the image down to make BlurHash faster
			smaller := image.NewRGBA(image.Rect(0, 0, img.Bounds().Max.X/6, img.Bounds().Max.Y/6))
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
		fileSize: f.Size(),
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

	return pma
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
	pmas.files = append(pmas.files, newPendingMessageAttachment(id, reader, func() { pmas.remove(id) }))
	pmas.Refresh()
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
