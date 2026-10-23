package ui

import (
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"github.com/bounce-chat/bounce/chat"
	log "github.com/sirupsen/logrus"
)

// Sending a message with a large attachment takes as long as hashing the file, so the engine reports the sent message
// only once that is done.  A pending send shows the user their message in the meantime, but only when sending takes
// long enough to notice: the pending bubble is drawn after a short delay, and a message the engine reports back
// before then is shown once, as the real thing, with no flicker.  When the real message arrives it takes the pending
// bubble's place.
const pendingSendDelay = 300 * time.Millisecond

type pendingSend struct {
	handle chat.ID // The bubble's ID while pending, local to this UI
	thread thread
	build  func(handle chat.ID) (*threadItem, error)
	shown  bool
	done   bool
}

type pendingSends struct {
	sync.Mutex
	byThread map[chat.ID][]*pendingSend // Oldest first
}

func newPendingSends() *pendingSends {
	return &pendingSends{byThread: map[chat.ID][]*pendingSend{}}
}

// beginPendingSend registers a message that is being sent on a thread.  build makes the bubble for it, and is only
// called if the engine has not reported the sent message before the delay passes.
func (ui *ui) beginPendingSend(t thread, build func(handle chat.ID) (*threadItem, error)) *pendingSend {
	ps := &pendingSend{handle: chat.NewID(), thread: t, build: build}
	ui.pendingSends.Lock()
	ui.pendingSends.byThread[t.getID()] = append(ui.pendingSends.byThread[t.getID()], ps)
	ui.pendingSends.Unlock()

	go func() {
		time.Sleep(pendingSendDelay)
		ui.pendingSends.Lock()
		defer ui.pendingSends.Unlock()
		if ps.done {
			return
		}
		ti, err := ps.build(ps.handle)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Warn("could not build a pending bubble for a message being sent")
			return
		}
		fyne.DoAndWait(func() {
			ui.appendThreadItem(ps.thread, ti)
			ps.thread.chatHistoryScroll().ScrollToBottom()
		})
		ps.shown = true
	}()
	return ps
}

// completePendingSend is called when the engine reports a sent message on a thread.  The oldest pending send on that
// thread is the one it answers; its bubble, if one was drawn, is removed so the real message can take its place.
// Must be called on the UI thread.
func (ui *ui) completePendingSend(t thread) {
	ui.pendingSends.Lock()
	defer ui.pendingSends.Unlock()
	queue := ui.pendingSends.byThread[t.getID()]
	if len(queue) == 0 {
		return
	}
	ps := queue[0]
	ui.pendingSends.byThread[t.getID()] = queue[1:]
	ps.done = true
	if ps.shown {
		ui.removePendingBubble(ps)
	}
}

// failPendingSend is called when the engine refused to send the message
func (ui *ui) failPendingSend(ps *pendingSend) {
	ui.pendingSends.Lock()
	defer ui.pendingSends.Unlock()
	if ps.done {
		return
	}
	ps.done = true
	queue := ui.pendingSends.byThread[ps.thread.getID()]
	for i, other := range queue {
		if other == ps {
			ui.pendingSends.byThread[ps.thread.getID()] = append(queue[:i], queue[i+1:]...)
			break
		}
	}
	if ps.shown {
		fyne.DoAndWait(func() { ui.removePendingBubble(ps) })
	}
}

func (ui *ui) removePendingBubble(ps *pendingSend) {
	ps.thread.chatHistoryScroll().deleteItem(ps.handle)
	ui.threads.removeItem(ps.handle)
	ui.messages.remove(ps.handle)
}

// outgoingAttachments turns the composer's attachments into what the engine sends, plus the attachment lists a
// pending bubble shows while the engine works.  The engine assigns the real IDs; the ones here are the composer's.
func outgoingAttachments(pending []*messageAttachment) (outgoing []chat.OutgoingAttachment, images []chat.ImageAttachment, files []chat.FileAttachment) {
	for _, pma := range pending {
		name := pma.reader.URI().Name()
		a := chat.OutgoingAttachment{
			Name:   name,
			Size:   pma.fileSize,
			Reader: pma.reader,
			Source: pma.reader.URI().Path(),
		}
		if pma.isImage {
			a.Image = &chat.ImageInfo{Width: pma.width, Height: pma.height, BlurHash: pma.blurHash}
			images = append(images, chat.ImageAttachment{ID: pma.id, Name: name, Size: pma.fileSize, Width: pma.width, Height: pma.height, BlurHash: pma.blurHash})
		} else {
			files = append(files, chat.FileAttachment{ID: pma.id, Name: name, Size: pma.fileSize})
		}
		outgoing = append(outgoing, a)
	}
	return outgoing, images, files
}
