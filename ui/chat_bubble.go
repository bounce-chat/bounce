package ui

import (
	"bytes"
	"image"
	"image/color"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/bbrks/go-blurhash"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var iconSize = float32(12)
var bufferSize = float32(30)
var imageAttachmentWidth = float32(300)
var unmergedVerticalBuffer = float32(4)

var mergeModeStandalone = 0
var mergeModeTop = 1
var mergeModeMiddle = 2
var mergeModeBottom = 3

var statePending = 0
var stateSynced = 1
var stateDelivered = 2
var stateRead = 3
var stateError = 4

var wordWrapRE = regexp.MustCompile(`\s*\S+`)

var imageAttachmentCache = map[string]image.Image{}
var imageAttachmentCacheMutex sync.Mutex

type chatBubble struct {
	widget.BaseWidget
	id               uuid.UUID
	outgoing         bool
	direct           bool
	writtenAt        int64
	mergeMode        int
	rawImages        []image.Image
	imageData        [][]byte
	username         *widget.RichText
	message          *widget.RichText
	imageAttachments *fyne.Container
	fileAttachments  *fyne.Container
	icon             *defaultImage
	background       *canvas.Rectangle
	decorations      *fyne.Container
	timestamp        *canvas.Text
	disappearingIcon *themedImage
	statusIcons      *fyne.Container
	pending          *themedImage
	synced           *themedImage
	delivered        *themedImage
	read             *themedImage
	errorIcon        *themedImage
	chunks           []string
	chunkLengths     []float32
	maxTextWidth     float32
	maxMessageWidth  float32
}

func newChatBubbleTemplate(fileGetter func(uuid.UUID) ([]byte, error)) *chatBubble {
	message := widget.NewRichText(&widget.TextSegment{
		Text: "",
		Style: widget.RichTextStyle{
			SizeName: theme.SizeNameText,
		},
	})
	message.Wrapping = fyne.TextWrapWord

	username := widget.NewRichText(
		&widget.TextSegment{
			Text: "",
			Style: widget.RichTextStyle{
				TextStyle: fyne.TextStyle{
					Bold: true,
				},
				SizeName: theme.SizeNameText,
			},
		},
	)
	username.Truncation = fyne.TextTruncateEllipsis
	username.Hide()

	timestamp := canvas.NewText("", theme.Color(theme.ColorNameForeground))
	timestamp.TextSize = theme.TextSize() * 0.6

	disappearingIcon := newThemedImage(
		newEmbeddedResource("assets/icons/chat_bubble/white/png/disappears.png"),
		newEmbeddedResource("assets/icons/chat_bubble/black/png/disappears.png"),
		iconSize,
		iconSize,
	)
	disappearingIcon.image.Translucency = 0.3
	disappearingIcon.Hide()

	pending := newThemedImage(
		newEmbeddedResource("assets/icons/chat_bubble/white/png/pending.png"),
		newEmbeddedResource("assets/icons/chat_bubble/black/png/pending.png"),
		iconSize,
		iconSize,
	)
	pending.image.Translucency = 0.3
	pending.Hide()

	synced := newThemedImage(
		newEmbeddedResource("assets/icons/chat_bubble/white/png/synced.png"),
		newEmbeddedResource("assets/icons/chat_bubble/black/png/synced.png"),
		iconSize,
		iconSize,
	)
	synced.image.Translucency = 0.3
	synced.Hide()

	delivered := newThemedImage(
		newEmbeddedResource("assets/icons/chat_bubble/white/png/delivered.png"),
		newEmbeddedResource("assets/icons/chat_bubble/black/png/delivered.png"),
		iconSize,
		iconSize,
	)
	delivered.image.Translucency = 0.3
	delivered.Hide()

	read := newThemedImage(
		newEmbeddedResource("assets/icons/chat_bubble/white/png/read.png"),
		newEmbeddedResource("assets/icons/chat_bubble/black/png/read.png"),
		iconSize,
		iconSize,
	)
	read.image.Translucency = 0.5
	read.Hide()

	errorIcon := newThemedImage(
		newEmbeddedResource("assets/icons/chat_bubble/white/png/error.png"),
		newEmbeddedResource("assets/icons/chat_bubble/black/png/error.png"),
		iconSize,
		iconSize,
	)
	errorIcon.image.Translucency = 0.3
	errorIcon.Hide()

	cb := &chatBubble{
		message:  message,
		username: username,
		icon: &defaultImage{
			size: theme.IconInlineSize(),
			foregroundText: &canvas.Text{
				Text:     "",
				TextSize: theme.IconInlineSize() / 2,
			},
			backgroundColor: canvas.NewImageFromImage(makeCircle(&colorRectangle{
				rect:  image.Rect(0, 0, int(theme.IconInlineSize())*8, int(theme.IconInlineSize())*8),
				color: color.Black,
			})),
			fileGetter: fileGetter,
			imageCache: make(map[uuid.UUID]*canvas.Image),
		},
		imageAttachments: container.New(NewRowWrapLayout()),
		fileAttachments:  container.NewVBox(),
		background: &canvas.Rectangle{
			CornerRadius: 15,
		},
		timestamp:        timestamp,
		disappearingIcon: disappearingIcon,
		statusIcons: container.NewStack(
			pending,
			synced,
			delivered,
			read,
			errorIcon,
		),
		pending:   pending,
		synced:    synced,
		delivered: delivered,
		read:      read,
		errorIcon: errorIcon,
	}
	cb.decorations = container.NewHBox(
		cb.timestamp,
		cb.disappearingIcon,
		cb.statusIcons,
	)

	cb.ExtendBaseWidget(cb)
	return cb
}

func (cb *chatBubble) setData(m *chatBubbleData) {
	cb.id = m.id
	cb.mergeMode = m.mergeMode
	cb.direct = m.direct

	colorName := colorNameIncomingChatBubble
	if m.outgoing {
		colorName = colorNameOutgoingChatBubble
	}
	cb.background.FillColor = theme.Color(colorName)

	if !m.direct && !m.outgoing {
		cb.username.Segments[0].(*widget.TextSegment).Text = m.username
		cb.username.Segments[0].(*widget.TextSegment).Style.ColorName = fyne.ThemeColorName("uuid:" + m.author.String())
		cb.username.Refresh()
		cb.username.Show()

		cb.icon.foregroundText.Text = m.initials
		cb.icon.foregroundText.Color = color.RGBA{0xff, 0xff, 0xff, 0xff}
		cb.icon.id = m.author
		cb.icon.images = m.iconImages
		cb.icon.clicked = func() {
			// TODO: bug: not clickable
			log.WithFields(log.Fields{
				"id":   m.author,
				"name": m.username,
			}).Info("user wants to open profile via icon")
		}
		cb.icon.setBackground()
		cb.icon.Refresh()
		cb.icon.Show()
	} else {
		cb.icon.Hide()
		cb.username.Hide()
	}

	if m.text != "" {
		cb.message.Segments[0].(*widget.TextSegment).Text = m.text
		cb.message.Show()
		cb.message.Refresh()
	} else {
		cb.message.Hide()
	}

	cb.rawImages = []image.Image{}
	cb.imageData = [][]byte{}
	cb.imageAttachments.Objects = []fyne.CanvasObject{}
	if len(m.imageAttachments) > 0 {
		size := imageAttachmentWidth / float32(len(m.imageAttachments))
		if size < 50 {
			size = float32(50)
		}
		if len(m.imageAttachments) > 1 {
			size -= theme.Padding()
		}

		for i, attachment := range m.imageAttachments {
			var rawImage image.Image
			var err error
			data, err := cb.icon.fileGetter(attachment.ID)
			if err != nil || len(data) == 0 {
				cacheKey := "blurhash-" + attachment.ID.String() + strconv.Itoa(attachment.Width) + strconv.Itoa(attachment.Height)
				var ok bool
				imageAttachmentCacheMutex.Lock()
				rawImage, ok = imageAttachmentCache[cacheKey]
				imageAttachmentCacheMutex.Unlock()
				if !ok {
					rawImage, err = blurhash.Decode(attachment.BlurHash, attachment.Width, attachment.Height, 1)
					if err != nil {
						log.WithFields(log.Fields{
							"error":    err.Error(),
							"blurhash": attachment.BlurHash,
						}).Error("invalid blurhash on image attachment")
						continue
					}
					imageAttachmentCacheMutex.Lock()
					imageAttachmentCache[cacheKey] = rawImage
					imageAttachmentCacheMutex.Unlock()
				}
			} else {
				cacheKey := attachment.ID.String() + strconv.Itoa(attachment.Width) + strconv.Itoa(attachment.Height)
				var ok bool
				imageAttachmentCacheMutex.Lock()
				rawImage, ok = imageAttachmentCache[cacheKey]
				imageAttachmentCacheMutex.Unlock()
				if !ok {
					rawImage, _, err = image.Decode(bytes.NewReader(data))
					if err != nil {
						log.WithFields(log.Fields{
							"blurhash": attachment.BlurHash,
						}).Error("invalid image data on image attachment")
						continue
					}
					imageAttachmentCacheMutex.Lock()
					imageAttachmentCache[cacheKey] = rawImage
					imageAttachmentCacheMutex.Unlock()
				}
			}

			cb.rawImages = append(cb.rawImages, rawImage)
			cb.imageData = append(cb.imageData, data)
			cb.imageAttachments.Add(
				newClickableImage(
					"",
					canvas.NewImageFromImage(rawImage),
					size,
					size,
					false,
					func() { m.imageDisplay(cb.rawImages, cb.imageData, i) },
				),
			)
		}
		cb.imageAttachments.Show()
		cb.imageAttachments.Refresh()
	} else {
		cb.imageAttachments.Hide()
	}
	cb.fileAttachments.Objects = []fyne.CanvasObject{}
	if len(m.fileAttachments) > 0 {
		for _, attachment := range m.fileAttachments {
			pma := &pendingMessageAttachment{
				id:       attachment.ID,
				reader:   nil,
				fileSize: attachment.Size,
				isImage:  false,
				icon:     canvas.NewImageFromResource(theme.FileIcon()),
				filename: widget.NewRichTextWithText(attachment.Name),
				size: &canvas.Text{
					Text:     fileSizeString(attachment.Size),
					TextSize: theme.TextSize() * 0.75,
					TextStyle: fyne.TextStyle{
						Italic: true,
					},
				},
				remove: widget.NewButtonWithIcon("", theme.DownloadIcon(), func() {
					// TODO: pop a file save dialog open, get the data from sqlite, and write it
				}),
			}
			pma.filename.Truncation = fyne.TextTruncateEllipsis
			pma.remove.Importance = widget.LowImportance
			pma.icon.FillMode = canvas.ImageFillContain

			pma.ExtendBaseWidget(pma)

			// TODO: if the file data isn't present in sqlite, disable the download button

			cb.fileAttachments.Add(pma)
		}

		cb.fileAttachments.Show()
		cb.fileAttachments.Refresh()
	} else {
		cb.fileAttachments.Hide()
	}

	cb.maxMessageWidth = fyne.MeasureText(
		longestLine(cb.message.Segments[0].(*widget.TextSegment).Text),
		theme.Size(cb.message.Segments[0].(*widget.TextSegment).Style.SizeName),
		cb.message.Segments[0].(*widget.TextSegment).Style.TextStyle,
	).Width
	usernameWidth := fyne.MeasureText(
		m.username,
		theme.Size(cb.username.Segments[0].(*widget.TextSegment).Style.SizeName),
		cb.username.Segments[0].(*widget.TextSegment).Style.TextStyle,
	).Width

	cb.outgoing = m.outgoing

	cb.writtenAt = m.writtenAt
	cb.updateDisplayTime()

	if m.expiresAt != 0 {
		cb.disappearingIcon.Show()
	} else {
		cb.disappearingIcon.Hide()
	}

	if !m.outgoing {
		cb.statusIcons.Hide()
	} else {
		switch m.state {
		case statePending:
			cb.pending.Show()
			cb.synced.Hide()
			cb.delivered.Hide()
			cb.read.Hide()
			cb.errorIcon.Hide()
		case stateSynced:
			cb.pending.Hide()
			cb.synced.Show()
			cb.delivered.Hide()
			cb.read.Hide()
			cb.errorIcon.Hide()
		case stateDelivered:
			cb.pending.Hide()
			cb.synced.Hide()
			cb.delivered.Show()
			cb.read.Hide()
			cb.errorIcon.Hide()
		case stateRead:
			cb.pending.Hide()
			cb.synced.Hide()
			cb.delivered.Hide()
			cb.read.Show()
			cb.errorIcon.Hide()
		case stateError:
			cb.pending.Hide()
			cb.synced.Hide()
			cb.delivered.Hide()
			cb.read.Hide()
			cb.errorIcon.Show()
		}
		cb.statusIcons.Show()
		cb.statusIcons.Refresh()
	}

	switch m.mergeMode {
	case mergeModeTop:
		if !m.outgoing {
			cb.icon.Hide()
		} else {
			cb.decorations.Show()
		}
		cb.decorations.Hide()
	case mergeModeMiddle:
		if !m.outgoing {
			cb.icon.Hide()
			cb.username.Hide()
		} else {
			cb.decorations.Show()
		}
		cb.decorations.Hide()
	case mergeModeBottom:
		if !m.outgoing {
			cb.username.Hide()
		}
		cb.decorations.Show()
	case mergeModeStandalone:
		cb.decorations.Show()
	}

	cb.maxTextWidth = cb.maxMessageWidth
	if cb.username.Visible() && usernameWidth > cb.maxMessageWidth {
		cb.maxTextWidth = usernameWidth
	}

	// Chunk out text for word wrapping calculations used to see if decorations need a new line
	lines := strings.Split(m.text, "\n")
	lastLine := lines[len(lines)-1]
	cb.chunks = wordWrapRE.FindAllString(lastLine, -1)

	cb.chunkLengths = []float32{}
	for _, c := range cb.chunks {
		cb.chunkLengths = append(
			cb.chunkLengths,
			fyne.MeasureText(
				c,
				theme.Size(cb.message.Segments[0].(*widget.TextSegment).Style.SizeName),
				cb.message.Segments[0].(*widget.TextSegment).Style.TextStyle,
			).Width,
		)
	}
}

func (cb *chatBubble) updateDisplayTime() {
	cb.timestamp.Text = timestampString(cb.writtenAt)
}

func (cb *chatBubble) CreateRenderer() fyne.WidgetRenderer {
	cb.ExtendBaseWidget(cb)

	return &chatBubbleRenderer{
		cb: cb,
	}
}

type chatBubbleRenderer struct {
	cb                   *chatBubble
	decorationsOnNewLine bool
}

func (cbr *chatBubbleRenderer) shiftForIcon() bool {
	if cbr.cb.icon.Visible() {
		return true
	}

	if !cbr.cb.outgoing && !cbr.cb.direct && (cbr.cb.mergeMode == mergeModeTop || cbr.cb.mergeMode == mergeModeMiddle) {
		return true
	}
	return false
}

func (cbr *chatBubbleRenderer) iconSize() float32 {
	return theme.IconInlineSize()
}

func (cbr *chatBubbleRenderer) Refresh() {
	cbr.cb.timestamp.Color = theme.Color(theme.ColorNameForeground)
	cbr.cb.updateDisplayTime()
	cbr.Layout(cbr.cb.Size())

	for _, obj := range cbr.Objects() {
		obj.Refresh()
	}
}

func (cbr *chatBubbleRenderer) Layout(size fyne.Size) {
	top := float32(0)
	bottom := size.Height
	switch cbr.cb.mergeMode {
	case mergeModeStandalone:
		top += unmergedVerticalBuffer
		size.Height -= unmergedVerticalBuffer * 2
		bottom -= unmergedVerticalBuffer
	case mergeModeTop:
		top += unmergedVerticalBuffer
		size.Height -= unmergedVerticalBuffer
	case mergeModeBottom:
		size.Height -= unmergedVerticalBuffer
		bottom -= unmergedVerticalBuffer
	}

	leftBorder := float32(0)
	rightBorder := size.Width

	if cbr.cb.outgoing {
		leftBorder += bufferSize
	} else {
		rightBorder -= bufferSize
		if cbr.shiftForIcon() {
			leftBorder += cbr.iconSize() + theme.Padding()
		}
	}

	usedWidth := cbr.cb.maxTextWidth + theme.Padding()*4
	if cbr.cb.imageAttachments.Visible() {
		usedWidth = imageAttachmentWidth + theme.Padding()*4
	}
	decorationsWidth := cbr.cb.decorations.MinSize().Width + theme.Padding()
	if cbr.cb.decorations.Visible() {
		cbr.decorationsOnNewLine = cbr.cb.decoratorNeedsNewLine(
			rightBorder-leftBorder-theme.Padding()*4,
			cbr.cb.decorations.MinSize().Width,
		)
		if cbr.decorationsOnNewLine {
			if decorationsWidth > cbr.cb.maxTextWidth {
				usedWidth = decorationsWidth + theme.Padding()*2
			}
		} else {
			messageAndDecorations := cbr.cb.maxMessageWidth + decorationsWidth + theme.Padding()*3
			if messageAndDecorations > usedWidth {
				if cbr.cb.imageAttachments.Visible() && messageAndDecorations > imageAttachmentWidth {
					cbr.decorationsOnNewLine = true
				} else {
					usedWidth = messageAndDecorations
				}
			}
		}
	}

	if cbr.cb.fileAttachments.Visible() {
		max := float32(0)
		for _, attachment := range cbr.cb.fileAttachments.Objects {
			ideal := attachment.(*pendingMessageAttachment).idealWidth() + theme.Padding()*4
			if ideal > max {
				max = ideal
			}
		}
		if max > usedWidth {
			if max > imageAttachmentWidth {
				usedWidth = imageAttachmentWidth + theme.Padding()*4
			} else {
				usedWidth = max
			}
		}
	}

	width := rightBorder - leftBorder
	if usedWidth < width {
		width = usedWidth
		if cbr.cb.outgoing {
			rightBorder = size.Width
			leftBorder = size.Width - width
		} else {
			rightBorder = leftBorder + width
		}
	}
	if cbr.cb.outgoing {
		rightBorder -= theme.Padding()
		leftBorder -= theme.Padding()
	}

	cbr.cb.background.Resize(fyne.Size{Height: size.Height, Width: width})
	cbr.cb.background.Move(fyne.Position{leftBorder, top})

	messageTop := top

	if cbr.cb.username.Visible() {
		cbr.cb.username.Resize(fyne.Size{Height: size.Height, Width: width + theme.Padding()*2})
		cbr.cb.username.Move(fyne.Position{leftBorder, top - theme.Padding()})
		messageTop += cbr.cb.username.MinSize().Height - theme.Padding()*2
	}

	if cbr.cb.imageAttachments.Visible() {
		cbr.cb.imageAttachments.Resize(fyne.Size{Height: size.Height, Width: width})
		cbr.cb.imageAttachments.Move(fyne.Position{leftBorder + theme.Padding()*2, messageTop + theme.Padding()*2})
		messageTop += cbr.cb.imageAttachments.MinSize().Height + theme.Padding()*2
	}

	if cbr.cb.fileAttachments.Visible() {
		cbr.cb.fileAttachments.Resize(fyne.Size{Height: size.Height, Width: width})
		cbr.cb.fileAttachments.Move(fyne.Position{leftBorder + theme.Padding()*2, messageTop + theme.Padding()*2})
		messageTop += cbr.cb.fileAttachments.MinSize().Height + theme.Padding()*2
	}

	if cbr.cb.decorations.Visible() {
		cbr.cb.decorations.Resize(cbr.cb.decorations.MinSize())
		cbr.cb.decorations.Move(fyne.Position{rightBorder - decorationsWidth, bottom - cbr.cb.decorations.MinSize().Height - theme.Padding()})
	}

	cbr.cb.message.Resize(fyne.Size{Height: size.Height, Width: width})
	cbr.cb.message.Move(fyne.Position{leftBorder, messageTop})

	if cbr.cb.icon.Visible() {
		cbr.cb.icon.Move(fyne.Position{0, bottom - cbr.iconSize()})
	}
}

func (cbr *chatBubbleRenderer) MinSize() fyne.Size {
	minSize := cbr.cb.message.MinSize()
	minSize.Width += bufferSize

	if cbr.cb.decorations.Visible() {
		if !cbr.decorationsOnNewLine {
			minSize.Width += cbr.cb.decorations.MinSize().Width + theme.Padding()
		} else {
			minSize.Height += cbr.cb.decorations.MinSize().Height + theme.Padding()
		}
	}

	if cbr.cb.username.Visible() {
		minSize.Height += cbr.cb.username.MinSize().Height - theme.Padding()*2
	}

	switch cbr.cb.mergeMode {
	case mergeModeStandalone:
		minSize.Height += unmergedVerticalBuffer * 2
	case mergeModeTop:
		minSize.Height += unmergedVerticalBuffer
	case mergeModeBottom:
		minSize.Height += unmergedVerticalBuffer
	}

	if cbr.cb.imageAttachments.Visible() {
		minSize.Height += cbr.cb.imageAttachments.MinSize().Height + theme.Padding()*2
		minSize.Width = imageAttachmentWidth + theme.Padding()*4 + bufferSize
	}

	if cbr.cb.fileAttachments.Visible() {
		minSize.Height += cbr.cb.fileAttachments.MinSize().Height + theme.Padding()*2
		if cbr.cb.fileAttachments.MinSize().Width > minSize.Width {
			minSize.Width = cbr.cb.fileAttachments.MinSize().Width + theme.Padding()*4 + bufferSize
		}
	}

	return minSize
}

func (cbr *chatBubbleRenderer) Objects() []fyne.CanvasObject {
	objs := []fyne.CanvasObject{
		cbr.cb.background,
		cbr.cb.decorations,
		cbr.cb.username,
		cbr.cb.message,
		cbr.cb.icon,
		cbr.cb.imageAttachments,
		cbr.cb.fileAttachments,
	}
	return objs
}

func (cbr *chatBubbleRenderer) Destroy() {}

func longestLine(message string) string {
	lines := strings.Split(message, "\n") // TODO: is '\n' too OS specific?  Maybe do this https://stackoverflow.com/a/49963413
	longest := ""
	for _, line := range lines {
		if len(line) > len(longest) {
			longest = line
		}
	}
	return longest
}

func (cb *chatBubble) decoratorNeedsNewLine(availableWidth, decoratorWidth float32) bool {
	currentWrap := float32(0)
	for i, l := range cb.chunkLengths {
		if currentWrap+l > availableWidth {
			if len(cb.chunks) <= i { // TODO: locking issue with chatHistory replacing the widget?
				return true
			}
			firstWord, _ := trimLeadingSpace(cb.chunks[i])
			currentWrap = fyne.MeasureText(
				firstWord,
				theme.Size(cb.message.Segments[0].(*widget.TextSegment).Style.SizeName),
				cb.message.Segments[0].(*widget.TextSegment).Style.TextStyle,
			).Width
		} else {
			currentWrap += l
		}
	}

	return currentWrap+decoratorWidth > availableWidth
}
