package ui

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

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
	"golang.org/x/image/draw"
)

var hyperlinkPattern = regexp.MustCompile(`(?:\w*://)?(?:[^/.\s]+\.)*[a-z]+(?:[.][\w]+|[:]\d+\b)(?:/[^/\s]+)*/?`)

var iconSize = float32(12)
var bufferSize = float32(30)
var imageAttachmentWidth = float32(300)
var unmergedVerticalBuffer = float32(4)

var maxRunes = 500

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

// Thumbnails of the image attachments shown in chat bubbles, so that scrolling back to a message neither reads its
// attachments from disk nor decodes them again
var imageAttachmentThumbnails = newThumbnailCache(64 << 20)

type chatBubble struct {
	widget.BaseWidget
	id                    chat.ID
	outgoing              bool
	direct                bool
	writtenAt             int64
	mergeMode             int
	username              *widget.RichText
	message               *widget.RichText
	imageAttachments      *fyne.Container
	fileAttachments       *fyne.Container
	icon                  *defaultImage
	background            *canvas.Rectangle
	decorations           *fyne.Container
	timestamp             *canvas.Text
	disappearingIcon      *themedImage
	statusIcons           *fyne.Container
	pending               *themedImage
	synced                *themedImage
	delivered             *themedImage
	read                  *themedImage
	errorIcon             *themedImage
	tooLong               bool
	readMore              *nohoverButton
	readMoreBackground    *canvas.Rectangle
	chunks                []string
	chunkLengths          []float32
	maxTextWidth          float32
	maxMessageWidth       float32
	fileIsDownloaded      func(chat.ID) bool
	fileIsEmbedded        func(chat.ID) bool
	fileIsWanted          func(chat.ID) bool
	saveFile              func(string, chat.ID)
	cancelDownload        func(chat.ID)
	getFileData           func(chat.ID) ([]byte, error)
	window                fyne.Window
	threads               *threadStore
	users                 *userStore
	openDM                func(*user) *directMessage
	showEditDMContainer   func(*directMessage)
	showDialog            func(dialog.Dialog, func())
	newChatBubbleTemplate func() *chatBubble
	openThread            func(*directMessage)
	showImageViewer       func([]*chat.ImageAttachment, int)
	mobileBack            func()
}

func (ui *ui) newChatBubbleTemplate() *chatBubble {
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
			fileGetter: ui.bounce.GetFileData,
		},
		imageAttachments: container.New(layout.NewRowWrapLayout()),
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
		pending:            pending,
		synced:             synced,
		delivered:          delivered,
		read:               read,
		errorIcon:          errorIcon,
		readMore:           newNoHoverButton("Read More", func() {}, nil),
		readMoreBackground: &canvas.Rectangle{},
		fileIsDownloaded:   ui.bounce.FileDownloaded,
		fileIsEmbedded:     ui.bounce.FileEmbedded,
		fileIsWanted:       ui.bounce.FileWanted,
		saveFile: func(name string, attachmentID chat.ID) {
			d := dialog.NewFileSave(func(writer fyne.URIWriteCloser, err error) {
				if err != nil {
					return
				}

				if writer == nil {
					return
				}

				if ui.bounce.FileEmbedded(attachmentID) {
					data, err := ui.bounce.GetFileData(attachmentID)
					if err != nil {
						log.WithFields(log.Fields{
							"error": err.Error(),
						}).Error("error getting data for file attachment")
						ui.showDialog(dialog.NewError(errors.New("error saving file: "+err.Error()), ui.window), nil)
					} else {
						writer.Write(data)
					}
					writer.Close()
				} else {
					writer.Close()
					os.Remove(writer.URI().Path())
					go ui.bounce.DownloadFileToDisk(attachmentID, writer.URI().Path())
				}
			}, ui.window)
			d.SetFileName(name)
			d.Show()
		},
		cancelDownload: func(attachmentID chat.ID) {
			ui.bounce.CancelDownload(attachmentID)

			messageID, ok := ui.messages.getMessageWithFile(attachmentID)
			if !ok {
				log.WithFields(log.Fields{
					"file_id": messageID,
				}).Warn("message not found with file while trying to cancel progress")
				return
			}

			t, ok := ui.threads.withItem(messageID)
			if !ok {
				log.WithFields(log.Fields{
					"message_id": messageID,
				}).Warn("thread not found with message")
				return
			}

			fyne.Do(func() {
				t.chatHistoryScroll().Refresh()
			})
		},
		getFileData:           ui.bounce.GetFileData,
		window:                ui.window,
		showDialog:            ui.showDialog,
		newChatBubbleTemplate: ui.newChatBubbleTemplate,
		threads:               ui.threads,
		users:                 ui.users,
		openDM:                ui.openDM,
		showEditDMContainer:   ui.showEditDMContainer,
		openThread: func(dm *directMessage) {
			ui.displayThread(dm)
			ui.bounce.UserConnectionDesired(dm.user.id)
			if fyne.CurrentDevice().IsMobile() {
				ui.state.viewStack = append(ui.state.viewStack, view{viewType: viewTypeThread, context: dm.user.id})
			}
			ui.state.currentView = viewTypeThread
		},
		showImageViewer: ui.showImageViewer,
		mobileBack:      ui.mobileBack,
	}
	cb.readMore.Importance = widget.LowImportance

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
	cb.tooLong = len([]rune(m.text)) > maxRunes

	colorName := colorNameIncomingChatBubble
	if m.outgoing {
		colorName = colorNameOutgoingChatBubble
	}
	cb.background.FillColor = theme.Color(colorName)
	cb.background.Refresh()

	if cb.tooLong {
		cb.readMore.OnTapped = func() {
			fullBubble := cb.newChatBubbleTemplate()
			fullBubble.setData(m)
			fullBubble.tooLong = false
			fullBubble.readMore.Hide()
			fullBubble.message.Segments[0].(*widget.TextSegment).Text = m.text
			fullBubble.message.Show()
			fullBubble.message.Refresh()
			fullBubble.mergeMode = mergeModeStandalone
			fullBubble.decorations.Show()
			fullBubble.Refresh()

			var displayFull dialog.Dialog
			closeButton := widget.NewButtonWithIcon("", theme.CancelIcon(), func() {
				displayFull.Dismiss()
			})
			closeButton.Importance = widget.LowImportance
			closeBar := container.New(
				layout.NewBorderLayout(nil, nil, nil, closeButton),
				closeButton,
			)

			bubbleScroll := container.NewVScroll(
				container.NewVBox(fullBubble),
			)
			content := container.New(
				layout.NewBorderLayout(closeBar, nil, nil, nil),
				closeBar,
				bubbleScroll,
			)

			displayFull = dialog.NewCustomWithoutButtons("Read More", content, cb.window)
			displayFull.Resize(cb.window.Canvas().Size())
			if fyne.CurrentDevice().IsMobile() {
				displayFull.Resize(fyne.Size{Width: cb.window.Canvas().Size().Width - 50, Height: cb.window.Canvas().Size().Height - 150})
			}
			displayFull.Refresh()
			cb.showDialog(displayFull, nil)
		}
		cb.readMore.Show()
		cb.readMoreBackground.FillColor = theme.Color(colorName)
		cb.readMoreBackground.Show()
	} else {
		cb.readMoreBackground.Hide()
		cb.readMore.Hide()
	}

	if !m.direct && !m.outgoing {
		cb.username.Segments[0].(*widget.TextSegment).Text = m.username
		cb.username.Segments[0].(*widget.TextSegment).Style.ColorName = fyne.ThemeColorName("id:" + m.author.String())
		cb.username.Refresh()
		cb.username.Show()

		cb.icon.foregroundText.Text = m.initials
		cb.icon.foregroundText.Color = color.RGBA{0xff, 0xff, 0xff, 0xff}
		cb.icon.id = m.author
		cb.icon.images = m.iconImages
		cb.icon.clicked = func() {
			var d dialog.Dialog
			viewImage := container.NewCenter(newDefaultImage(m.author, m.iconImages, m.initials, 128, cb.getFileData, func() {
				if len(m.iconImages) == 0 {
					return
				}
				imageID := m.iconImages[len(m.iconImages)-1]
				if _, err := cb.getFileData(imageID); err != nil {
					return
				}
				if fyne.CurrentDevice().IsMobile() {
					cb.mobileBack()
				} else {
					d.Dismiss()
				}
				cb.showImageViewer([]*chat.ImageAttachment{{ID: imageID}}, 0)
			}))
			name := widget.NewLabel(m.username)
			name.Truncation = fyne.TextTruncateEllipsis
			name.Alignment = fyne.TextAlignCenter
			sizedName := container.New(
				newMinWidthLayout(150),
				name,
			)
			message := widget.NewButton("Message", func() {
				dm, ok := cb.threads.getDM(m.author)
				if !ok {
					u, ok := cb.users.get(m.author)
					if !ok {
						log.WithFields(log.Fields{
							"user_id": m.author,
						}).Error("cannot open DM for unknown user")
						return
					}
					dm = cb.openDM(u)
				}
				if fyne.CurrentDevice().IsMobile() {
					cb.mobileBack()
				} else {
					d.Dismiss()
				}
				cb.openThread(dm)
			})
			editUser := widget.NewButton("Edit User", func() {
				dm, ok := cb.threads.getDM(m.author)
				if !ok {
					u, ok := cb.users.get(m.author)
					if !ok {
						log.WithFields(log.Fields{
							"user_id": m.author,
						}).Error("cannot open DM for unknown user")
						return
					}
					dm = cb.openDM(u)
				}
				if fyne.CurrentDevice().IsMobile() {
					cb.mobileBack()
				} else {
					d.Dismiss()
				}
				cb.showEditDMContainer(dm)
			})
			closeButton := widget.NewButton("Close", func() {
				if fyne.CurrentDevice().IsMobile() {
					cb.mobileBack()
				} else {
					d.Dismiss()
				}
			})
			content := container.NewVBox(
				viewImage,
				sizedName,
				message,
				editUser,
				closeButton,
			)
			d = dialog.NewCustomWithoutButtons("User Details", content, cb.window)
			cb.showDialog(d, nil)
		}
		cb.icon.setBackground()
		cb.icon.Refresh()
		cb.icon.Show()
	} else {
		cb.icon.Hide()
		cb.username.Hide()
	}

	if m.text != "" {
		if cb.tooLong {
			cb.message.Segments = hyperlinkify(string([]rune(m.text)[:maxRunes]) + "...")
		} else {
			cb.message.Segments = hyperlinkify(m.text)
		}
		cb.message.Show()
		cb.message.Refresh()
	} else {
		cb.message.Hide()
	}

	cb.imageAttachments.Objects = []fyne.CanvasObject{}
	if len(m.imageAttachments) > 0 {
		size := imageAttachmentWidth / float32(len(m.imageAttachments))
		if size < 50 {
			size = float32(50)
		}
		if len(m.imageAttachments) > 1 {
			size -= theme.Padding()
		}
		if len(m.imageAttachments) == 1 && m.imageAttachments[0].Width < int(size) {
			size = float32(m.imageAttachments[0].Width)
			if size < 50 {
				size = float32(50)
			}
		}

		for i, attachment := range m.imageAttachments {
			thumb, err := cb.thumbnail(attachment, size)
			if err != nil {
				log.WithFields(log.Fields{
					"error":   err.Error(),
					"file_id": attachment.ID,
				}).Error("cannot display image attachment")
				continue
			}
			cb.imageAttachments.Add(
				newClickableImage(
					"",
					canvas.NewImageFromImage(thumb),
					size,
					size,
					false,
					func() { m.imageDisplay(m.imageAttachments, i) },
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
			pma := newMessageAttachment(attachment.ID, attachment.Name, attachment.Size)

			if !cb.fileIsEmbedded(attachment.ID) && cb.fileIsWanted(attachment.ID) && !cb.fileIsDownloaded(attachment.ID) {
				pma.progress.Value = attachment.Progress
				pma.progress.Show()
				pma.action = newNoHoverButton("", func() { cb.cancelDownload(attachment.ID) }, theme.CancelIcon())
			} else {
				pma.action = newNoHoverButton("", func() { cb.saveFile(attachment.Name, attachment.ID) }, theme.DownloadIcon())
				pma.progress.Hide()
			}
			pma.action.Importance = widget.LowImportance

			if !cb.fileIsEmbedded(attachment.ID) || (cb.fileIsEmbedded(attachment.ID) && cb.fileIsDownloaded(attachment.ID)) {
				pma.action.Enable()
			} else {
				pma.action.Disable()
			}

			cb.fileAttachments.Add(pma)
		}

		cb.fileAttachments.Show()
		cb.fileAttachments.Refresh()
	} else {
		cb.fileAttachments.Hide()
	}

	cb.maxMessageWidth = fyne.MeasureText(
		longestLine(m.text),
		theme.Size(theme.SizeNameText),
		fyne.TextStyle{},
	).Width
	usernameWidth := fyne.MeasureText(
		m.username,
		theme.Size(cb.username.Segments[0].(*widget.TextSegment).Style.SizeName),
		cb.username.Segments[0].(*widget.TextSegment).Style.TextStyle,
	).Width + theme.Padding()

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
		}
		cb.decorations.Hide()
	case mergeModeMiddle:
		if !m.outgoing {
			cb.icon.Hide()
			cb.username.Hide()
		}
		cb.decorations.Hide()
	case mergeModeBottom:
		if !m.outgoing {
			cb.username.Hide()
		}
		cb.decorations.Refresh()
		if cb.tooLong {
			cb.decorations.Hide()
		} else {
			cb.decorations.Show()
		}
	case mergeModeStandalone:
		cb.decorations.Refresh()
		if cb.tooLong {
			cb.decorations.Hide()
		} else {
			cb.decorations.Show()
		}
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
				theme.Size(theme.SizeNameText),
				fyne.TextStyle{},
			).Width,
		)
	}
}

func (cb *chatBubble) updateDisplayTime() {
	cb.timestamp.Text = timestampString(cb.writtenAt, time.Now())
}

func (cb *chatBubble) Tapped(e *fyne.PointEvent) {
	if cb.icon.Visible() {
		if e.Position.X > theme.Padding() && e.Position.X < cb.icon.MinSize().Width+theme.Padding() {
			if e.Position.Y > cb.Size().Height-cb.icon.MinSize().Height && e.Position.Y < cb.Size().Height {
				if cb.icon.clicked != nil {
					cb.icon.clicked()
				}
			}
		}
	}
}

func (cb *chatBubble) TappedSecondary(*fyne.PointEvent) {
	//TODO: open the reply / react / etc menu
}

func (cb *chatBubble) CreateRenderer() fyne.WidgetRenderer {
	cb.ExtendBaseWidget(cb)

	return &chatBubbleRenderer{
		cb: cb,
		objects: []fyne.CanvasObject{
			cb.background,
			cb.decorations,
			cb.username,
			cb.message,
			cb.icon,
			cb.imageAttachments,
			cb.fileAttachments,
			cb.readMoreBackground,
			cb.readMore,
		},
	}
}

type chatBubbleRenderer struct {
	cb                   *chatBubble
	objects              []fyne.CanvasObject
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
	// Define the largest possible bounds of the chat bubble within this size
	top := float32(0)
	bottom := size.Height
	left := float32(0) + theme.Padding()
	right := size.Width - theme.Padding()

	switch cbr.cb.mergeMode {
	case mergeModeStandalone:
		top += unmergedVerticalBuffer
		bottom -= unmergedVerticalBuffer
	case mergeModeTop:
		top += unmergedVerticalBuffer
	case mergeModeBottom:
		bottom -= unmergedVerticalBuffer
	}

	if cbr.cb.outgoing {
		left += bufferSize
	} else {
		right -= bufferSize
		if cbr.shiftForIcon() {
			left += cbr.iconSize() + theme.Padding()
		}
	}

	// Figure out the widest we would want to make this chat bubble
	maximumDesiredWidth := cbr.cb.maxTextWidth + theme.Padding()*4

	decorationsWidth := cbr.cb.decorations.MinSize().Width + theme.Padding()
	if cbr.cb.decorations.Visible() {
		cbr.decorationsOnNewLine = cbr.cb.decoratorNeedsNewLine(
			right-left-theme.Padding()*4,
			cbr.cb.decorations.MinSize().Width,
		)
		if cbr.decorationsOnNewLine {
			if decorationsWidth > maximumDesiredWidth {
				maximumDesiredWidth = decorationsWidth + theme.Padding()*2
			}
		} else {
			messageAndDecorations := cbr.cb.maxMessageWidth + decorationsWidth + theme.Padding()*4
			if messageAndDecorations > maximumDesiredWidth {
				maximumDesiredWidth = messageAndDecorations
			}
		}
	}

	if cbr.cb.fileAttachments.Visible() {
		max := float32(0)
		for _, attachment := range cbr.cb.fileAttachments.Objects {
			ideal := attachment.(*messageAttachment).idealWidth() + theme.Padding()*4
			if ideal > max {
				max = ideal
			}
		}
		if max > maximumDesiredWidth {
			if max > imageAttachmentWidth {
				maximumDesiredWidth = imageAttachmentWidth + theme.Padding()*4
			} else {
				maximumDesiredWidth = max
			}
		}
	}

	if cbr.cb.imageAttachments.Visible() {
		maximumDesiredWidth = imageAttachmentWidth + theme.Padding()*4
	}

	width := right - left
	height := bottom - top
	if maximumDesiredWidth < width {
		width = maximumDesiredWidth
	}
	if cbr.cb.outgoing {
		left = size.Width - width
		// Make room for scroll bar
		left -= theme.Padding()
		right -= theme.Padding()
	} else {
		right = left + width
	}

	// Place the background and icon
	cbr.cb.background.Resize(fyne.Size{Height: height, Width: width})
	cbr.cb.background.Move(fyne.Position{X: left, Y: top})

	if cbr.cb.icon.Visible() {
		cbr.cb.icon.Move(fyne.Position{X: theme.Padding(), Y: bottom - cbr.iconSize()})
	}

	// Place each widget from top to bottom
	topEdge := top
	if cbr.cb.username.Visible() {
		cbr.cb.username.Resize(fyne.Size{Height: height, Width: width})
		cbr.cb.username.Move(fyne.Position{X: left, Y: top})
		topEdge += cbr.cb.username.MinSize().Height
	}

	if cbr.cb.imageAttachments.Visible() {
		if !cbr.cb.username.Visible() {
			topEdge += theme.Padding()
		}
		cbr.cb.imageAttachments.Resize(fyne.Size{Height: height, Width: width})
		cbr.cb.imageAttachments.Move(fyne.Position{X: left + theme.Padding()*2, Y: topEdge})
		topEdge += cbr.cb.imageAttachments.MinSize().Height
	}

	if cbr.cb.fileAttachments.Visible() {
		if cbr.cb.imageAttachments.Visible() || (!cbr.cb.imageAttachments.Visible() && !cbr.cb.username.Visible() && (cbr.cb.decorations.Visible() || cbr.cb.message.Visible())) {
			topEdge += theme.Padding()
		}
		cbr.cb.fileAttachments.Resize(fyne.Size{Height: height, Width: width})
		cbr.cb.fileAttachments.Move(fyne.Position{X: left + theme.Padding()*2, Y: topEdge})
		topEdge += cbr.cb.fileAttachments.MinSize().Height
	}

	if cbr.cb.message.Visible() {
		cbr.cb.message.Resize(fyne.Size{Height: height, Width: width})
		cbr.cb.message.Move(fyne.Position{X: left, Y: topEdge})
	}

	if cbr.cb.decorations.Visible() {
		cbr.cb.decorations.Resize(cbr.cb.decorations.MinSize())
		cbr.cb.decorations.Move(fyne.Position{X: right - decorationsWidth + theme.Padding(), Y: bottom - cbr.cb.decorations.MinSize().Height - theme.Padding()})
	} else if cbr.cb.readMore.Visible() {
		cbr.cb.readMore.Resize(cbr.cb.readMore.MinSize())
		cbr.cb.readMore.Move(fyne.Position{X: right - cbr.cb.readMore.MinSize().Width + theme.Padding(), Y: bottom - cbr.cb.readMore.MinSize().Height})
		cbr.cb.readMoreBackground.Resize(fyne.Size{Width: cbr.cb.readMore.Size().Width - theme.Padding()*2, Height: cbr.cb.readMore.Size().Height - theme.Padding()*5})
		cbr.cb.readMoreBackground.Move(fyne.Position{X: right - cbr.cb.readMore.MinSize().Width + theme.Padding(), Y: bottom - cbr.cb.readMore.MinSize().Height + theme.Padding()*3})
	}
}

func (cbr *chatBubbleRenderer) MinSize() fyne.Size {
	minSize := fyne.Size{}

	if cbr.cb.imageAttachments.Visible() {
		minSize.Width = imageAttachmentWidth + theme.Padding()*4
	} else {
		minSize.Width = cbr.cb.maxTextWidth + theme.Padding()*4
	}

	if cbr.cb.fileAttachments.Visible() {
		max := float32(0)
		for _, attachment := range cbr.cb.fileAttachments.Objects {
			ideal := attachment.(*messageAttachment).idealWidth() + theme.Padding()*4
			if ideal > max {
				max = ideal
			}
		}
		if max > minSize.Width {
			if max > imageAttachmentWidth {
				minSize.Width = imageAttachmentWidth + theme.Padding()*4
			} else {
				minSize.Width = max
			}
		}
	}

	decorationsWidth := cbr.cb.decorations.MinSize().Width + theme.Padding()
	if cbr.cb.decorations.Visible() {
		if cbr.decorationsOnNewLine {
			if decorationsWidth > minSize.Width {
				minSize.Width = decorationsWidth + theme.Padding()*2
			}
		} else {
			messageAndDecorations := cbr.cb.maxMessageWidth + decorationsWidth + theme.Padding()*3
			if messageAndDecorations > minSize.Width {
				minSize.Width = messageAndDecorations
			}
		}
	}
	minSize.Width += bufferSize

	switch cbr.cb.mergeMode {
	case mergeModeStandalone:
		minSize.Height += unmergedVerticalBuffer * 2
	case mergeModeTop:
		minSize.Height += unmergedVerticalBuffer
	case mergeModeBottom:
		minSize.Height += unmergedVerticalBuffer
	}

	if cbr.cb.username.Visible() {
		minSize.Height += cbr.cb.username.MinSize().Height
	}

	if cbr.cb.imageAttachments.Visible() {
		if !cbr.cb.username.Visible() {
			minSize.Height += theme.Padding()
		}
		minSize.Height += cbr.cb.imageAttachments.MinSize().Height
		if !cbr.cb.fileAttachments.Visible() && !cbr.cb.message.Visible() && !cbr.cb.decorations.Visible() {
			minSize.Height += theme.Padding()
		}
	}

	if cbr.cb.fileAttachments.Visible() {
		if cbr.cb.imageAttachments.Visible() || (!cbr.cb.imageAttachments.Visible() && !cbr.cb.username.Visible() && (cbr.cb.decorations.Visible() || cbr.cb.message.Visible())) {
			minSize.Height += theme.Padding()
		}
		minSize.Height += cbr.cb.fileAttachments.MinSize().Height
	}

	if cbr.cb.message.Visible() {
		minSize.Height += cbr.cb.message.MinSize().Height
	}
	if cbr.cb.decorations.Visible() {
		if cbr.cb.message.Visible() {
			if !cbr.decorationsOnNewLine {
				minSize.Width += cbr.cb.decorations.MinSize().Width + theme.Padding()
			} else {
				minSize.Height += cbr.cb.decorations.MinSize().Height
			}
		} else {
			minSize.Height += cbr.cb.decorations.MinSize().Height + theme.Padding()
		}
	}

	if minSize.Width > 50 {
		minSize.Width = 50
	}
	return minSize

}

func (cbr *chatBubbleRenderer) Objects() []fyne.CanvasObject {
	return cbr.objects
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
			firstWord := strings.TrimLeftFunc(cb.chunks[i], unicode.IsSpace)
			currentWrap = fyne.MeasureText(
				firstWord,
				theme.Size(theme.SizeNameText),
				fyne.TextStyle{},
			).Width
		} else {
			currentWrap += l
		}
	}

	return currentWrap+decoratorWidth > availableWidth
}

func hyperlinkify(text string) []widget.RichTextSegment {
	segments := []widget.RichTextSegment{}

	allIndexes := hyperlinkPattern.FindAllSubmatchIndex([]byte(text), -1)
	matchCount := len(allIndexes)

	if len(allIndexes) == 0 {
		return []widget.RichTextSegment{
			&widget.TextSegment{
				Text: text,
				Style: widget.RichTextStyle{
					SizeName: theme.SizeNameText,
					Inline:   true,
				},
			},
		}
	}

	start := 0
	for i, loc := range allIndexes {
		terminal := string(text[loc[1]-1])
		end := loc[1]
		stripped := ""
		if terminal == "." || terminal == "," {
			end -= 1
			stripped = terminal
			terminal = string(text[loc[1]-2])
		}

		hyperlinkText := string(text[loc[0]:end])
		if terminal == ")" && !parenthesisAreBalanced(hyperlinkText) {
			// The link ends with a parenthesis but the URL suggests that the author
			// didn't intend for that to be part of the link
			end -= 1
			stripped = terminal + stripped
			hyperlinkText = string(text[loc[0]:end])
		}

		if loc[0] > start {
			segments = append(segments, &widget.TextSegment{
				Text: string(text[start:loc[0]]),
				Style: widget.RichTextStyle{
					SizeName: theme.SizeNameText,
					Inline:   true,
				},
			})
		}
		start = end

		parse := hyperlinkText
		if !strings.Contains(hyperlinkText, "://") {
			parse = "http://" + hyperlinkText
		}
		parsed, err := url.Parse(parse)
		if err == nil {
			if parsed.Scheme == "" {
				parsed.Scheme = "http"
			}
			segments = append(segments,
				&widget.HyperlinkSegment{
					Text: hyperlinkText,
					URL:  parsed,
				},
			)
		} else {
			segments = append(segments, &widget.TextSegment{
				Text: hyperlinkText,
				Style: widget.RichTextStyle{
					SizeName: theme.SizeNameText,
					Inline:   true,
				},
			})

		}

		if i == matchCount-1 {
			if loc[1] < len(text) {
				segments = append(segments, &widget.TextSegment{
					Text: string(text[end:len(text)]),
					Style: widget.RichTextStyle{
						SizeName: theme.SizeNameText,
						Inline:   true,
					},
				})
			} else if stripped != "" {
				segments = append(segments, &widget.TextSegment{
					Text: stripped,
					Style: widget.RichTextStyle{
						SizeName: theme.SizeNameText,
						Inline:   true,
					},
				})
			}
		}
	}

	if len(segments) == 0 {
		segments = append(segments, &widget.TextSegment{
			Text: "",
			Style: widget.RichTextStyle{
				SizeName: theme.SizeNameText,
			},
		})
	}

	return segments
}

func parenthesisAreBalanced(text string) bool {
	stack := []rune{}

	for _, c := range text {
		if c == '(' {
			stack = append(stack, c)
		} else if c == ')' {
			if len(stack) == 0 {
				return false
			}
			end := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if end != '(' {
				return false
			}
		}
	}

	return len(stack) == 0
}

// The width a blurhash is decoded at before being scaled up to a thumbnail; its gradients survive the scaling
var blurhashDecodeWidth = float32(32)

// A thumbnail is never taller than this many times its width: real pictures almost never are, but the dimensions
// of an attachment that has not been downloaded are whatever the sender claimed
var maximumThumbnailAspect = float32(10)

// thumbnail returns the attachment scaled to the given width, from the cache when it was shown before.  The file
// is only read and decoded on a miss, and the full size decode is dropped as soon as it has been scaled.  Until the
// file has been downloaded its blurhash stands in.
func (cb *chatBubble) thumbnail(attachment *chat.ImageAttachment, width float32) (image.Image, error) {
	key := attachment.ID.String()
	placeholderKey := "blurhash-" + key
	if thumb, ok := imageAttachmentThumbnails.get(key); ok {
		return thumb, nil
	}

	data, err := cb.icon.fileGetter(attachment.ID)
	downloaded := err == nil && len(data) > 0
	if downloaded {
		decoded, _, err := image.Decode(bytes.NewReader(data))
		if err == nil {
			thumb := fitThumbnail(decoded, width)
			imageAttachmentThumbnails.put(key, thumb)
			imageAttachmentThumbnails.remove(placeholderKey)
			return thumb, nil
		}
		log.WithFields(log.Fields{
			"error":   err.Error(),
			"file_id": attachment.ID,
		}).Warn("invalid image data on image attachment, showing its blurhash instead")
	}

	placeholder, ok := imageAttachmentThumbnails.get(placeholderKey)
	if !ok {
		decoded, err := blurhash.Decode(attachment.BlurHash, int(blurhashDecodeWidth), scaledHeight(blurhashDecodeWidth, attachment.Width, attachment.Height), 1)
		if err != nil {
			return nil, errors.New("invalid blurhash: " + err.Error())
		}
		placeholder = fitThumbnail(decoded, width)
		imageAttachmentThumbnails.put(placeholderKey, placeholder)
	}
	if downloaded {
		// The file will not decode any better next time, so remember the blurhash as this attachment's thumbnail
		// rather than reading the file again on every refresh
		imageAttachmentThumbnails.put(key, placeholder)
	}
	return placeholder, nil
}

// fitThumbnail scales an image to the given width in proportion.  One taller than maximumThumbnailAspect allows
// is letterboxed in a canvas of the maximum height instead, so that no attachment can make a bubble, or its entry
// in the cache, arbitrarily tall.
func fitThumbnail(img image.Image, width float32) *image.RGBA {
	bounds := img.Bounds()
	height := scaledHeight(width, bounds.Dx(), bounds.Dy())
	canvas := image.NewRGBA(image.Rect(0, 0, int(width), height))

	target := canvas.Rect
	if float32(bounds.Dy()) > float32(height)/width*float32(bounds.Dx()) {
		fitted := int(float32(height) * float32(bounds.Dx()) / float32(bounds.Dy()))
		if fitted < 1 {
			fitted = 1
		}
		left := (int(width) - fitted) / 2
		target = image.Rect(left, 0, left+fitted, height)
	}
	draw.ApproxBiLinear.Scale(canvas, target, img, bounds, draw.Over, nil)
	return canvas
}

// scaledHeight is the height that keeps an image of the given source dimensions in proportion at the given width,
// never more than maximumThumbnailAspect times the width.  Missing dimensions give a square.
func scaledHeight(width float32, sourceWidth, sourceHeight int) int {
	if sourceWidth <= 0 || sourceHeight <= 0 {
		return int(width)
	}
	aspect := float32(sourceHeight) / float32(sourceWidth)
	if aspect > maximumThumbnailAspect {
		aspect = maximumThumbnailAspect
	}
	height := int(aspect * width)
	if height < 1 {
		return 1
	}
	return height
}
