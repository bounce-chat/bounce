package ui

import (
	"image"
	"image/color"
	"regexp"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var iconSize = float32(12)
var bufferSize = float32(30)
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

type chatBubble struct {
	widget.BaseWidget
	id               uuid.UUID
	outgoing         bool
	direct           bool
	writtenAt        int64
	mergeMode        int
	username         *widget.RichText
	message          *widget.RichText
	icon             *defaultImage
	background       *canvas.Rectangle
	decorations      *fyne.Container
	timestamp        *canvas.Text
	disappearingIcon *canvas.Image
	statusIcons      *fyne.Container
	pending          *canvas.Image
	synced           *canvas.Image
	delivered        *canvas.Image
	read             *canvas.Image
	errorIcon        *canvas.Image
	chunks           []string
	chunkLengths     []float32
	maxTextWidth     float32
	maxMessageWidth  float32
}

func newChatBubbleTemplate() *chatBubble {
	message := widget.NewRichText(&widget.TextSegment{
		Text: "",
		Style: widget.RichTextStyle{
			SizeName: theme.SizeNameText,
		},
	})
	message.Wrapping = fyne.TextWrapWord

	// TODO: use rich text with color to get truncation https://github.com/fyne-io/fyne/issues/5306
	//username := widget.NewRichText(&widget.TextSegment{
	//	Text: "",
	//})
	//username.Truncation = fyne.TextTruncateEllipsis
	//username.Segments[0].(*widget.TextSegment).Style.TextStyle.Bold = true
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

	disappearingIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/icons/chat_bubble/white/png/disappears.png"))
	disappearingIcon.Translucency = 0.3
	disappearingIcon.FillMode = canvas.ImageFillContain
	disappearingIcon.SetMinSize(fyne.Size{iconSize, iconSize})
	disappearingIcon.Hide()

	pending := canvas.NewImageFromResource(newEmbeddedResource("assets/icons/chat_bubble/white/png/pending.png"))
	pending.Translucency = 0.3
	pending.FillMode = canvas.ImageFillContain
	pending.SetMinSize(fyne.Size{iconSize, iconSize})
	pending.Hide()

	synced := canvas.NewImageFromResource(newEmbeddedResource("assets/icons/chat_bubble/white/png/synced.png"))
	synced.Translucency = 0.3
	synced.FillMode = canvas.ImageFillContain
	synced.SetMinSize(fyne.Size{iconSize, iconSize})
	synced.Hide()

	delivered := canvas.NewImageFromResource(newEmbeddedResource("assets/icons/chat_bubble/white/png/delivered.png"))
	delivered.Translucency = 0.3
	delivered.FillMode = canvas.ImageFillContain
	delivered.SetMinSize(fyne.Size{iconSize, iconSize})
	delivered.Hide()

	read := canvas.NewImageFromResource(newEmbeddedResource("assets/icons/chat_bubble/white/png/read.png"))
	read.Translucency = 0.5
	read.FillMode = canvas.ImageFillContain
	read.SetMinSize(fyne.Size{iconSize, iconSize})
	read.Hide()

	errorIcon := canvas.NewImageFromResource(newEmbeddedResource("assets/icons/chat_bubble/white/png/error.png"))
	errorIcon.Translucency = 0.3
	errorIcon.FillMode = canvas.ImageFillContain
	errorIcon.SetMinSize(fyne.Size{iconSize, iconSize})
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
				rect: image.Rect(0, 0, int(theme.IconInlineSize())*8, int(theme.IconInlineSize())*8),
			})),
		},
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
		cb.icon.backgroundColor = canvas.NewImageFromImage(makeCircle(&colorRectangle{
			rect:  image.Rect(0, 0, int(theme.IconInlineSize())*8, int(theme.IconInlineSize())*8),
			color: uuidToColor(m.author), // TODO: access this color without recreating the whole thing?
		}))
		cb.icon.foregroundText.Color = color.RGBA{0xff, 0xff, 0xff, 0xff}
		cb.icon.clicked = func() {
			// TODO: bug: not clickable
			log.WithFields(log.Fields{
				"id":   m.author,
				"name": m.username,
			}).Info("user wants to open profile via icon")
		}
		cb.icon.Show()
		cb.icon.Refresh()
	} else {
		cb.icon.Hide()
		cb.username.Hide()
	}

	cb.message.Segments[0].(*widget.TextSegment).Text = m.text
	cb.message.Refresh()

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
	cbr.cb.updateDisplayTime()
	cbr.Layout(cbr.cb.Size())
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
	decorationsWidth := cbr.cb.decorations.MinSize().Width + theme.Padding()
	if cbr.cb.decorations.Visible() {
		fits := cbr.cb.guessIfDecoratorFits(
			rightBorder-leftBorder-theme.Padding()*4,
			cbr.cb.decorations.MinSize().Width,
		)
		cbr.decorationsOnNewLine = !fits
		if !cbr.decorationsOnNewLine {
			messageAndDecorations := cbr.cb.maxMessageWidth + decorationsWidth + theme.Padding()*3
			if messageAndDecorations > usedWidth {
				usedWidth = messageAndDecorations
			}
		} else {
			if decorationsWidth > cbr.cb.maxTextWidth {
				usedWidth = decorationsWidth + theme.Padding()*2
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
		cbr.cb.username.Move(fyne.Position{leftBorder, top + theme.Padding()})
		messageTop += cbr.cb.username.MinSize().Height
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

	if cbr.cb.decorations.Visible() {
		if !cbr.decorationsOnNewLine {
			minSize.Width += cbr.cb.decorations.MinSize().Width + theme.Padding()
		} else {
			minSize.Height += cbr.cb.decorations.MinSize().Height + theme.Padding()
		}
	}

	if cbr.cb.username.Visible() {
		minSize.Height += cbr.cb.username.MinSize().Height
	}

	switch cbr.cb.mergeMode {
	case mergeModeStandalone:
		minSize.Height += unmergedVerticalBuffer * 2
	case mergeModeTop:
		minSize.Height += unmergedVerticalBuffer
	case mergeModeBottom:
		minSize.Height += unmergedVerticalBuffer
	}

	return minSize
}

func (cbr *chatBubbleRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{
		cbr.cb.background,
		cbr.cb.decorations,
		cbr.cb.username,
		cbr.cb.message,
		cbr.cb.icon,
	}
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

func (cb *chatBubble) guessIfDecoratorFits(availableWidth, decoratorWidth float32) bool {
	currentWrap := float32(0)
	for i, l := range cb.chunkLengths {
		if currentWrap+l > availableWidth {
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

	return currentWrap+decoratorWidth < availableWidth
}
