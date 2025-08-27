package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
)

type invite struct {
	id        uuid.UUID
	name      *widget.Label
	view      *fyne.Container
	button    *threadButton
	timestamp int64
	// TODO: widgets for the things, name, etc
}

func (ui *ui) buildNewGroupChatInvite(bounceGroup chat.Group) {
	i := &invite{
		name: widget.NewLabel(bounceGroup.Name),
	}

	i.view = container.NewCenter(
		container.NewVBox(
			i.name,
		),
	)

	ui.threads.add(bounceGroup.ID, i)
	ui.refreshThreadOrder()
}

func (i *invite) getID() uuid.UUID {
	return i.id
}

func (i *invite) getView() *fyne.Container {
	return i.view
}

func (i *invite) getEntry() *threadEntry {
	return nil
}

func (i *invite) chatHistoryScroll() *chatHistory {
	return nil
}

func (i *invite) getButton() *threadButton {
	return i.button
}

func (i *invite) getTypingIndicator() *typingIndicator {
	return nil
}

func (i *invite) getLastMessageTime() int64 {
	return i.timestamp
}

func (i *invite) setLastMessageTime(timestamp int64) {

}

func (i *invite) getNotificationsMutedUntil() int64 {
	return 0
}

func (i *invite) refreshReadReceiptSettingSelection([]string) {}

func (i *invite) refreshTypingIndicatorSettingSelection([]string) {}

func (i *invite) getEditIcon() *defaultImage {
	return nil
}

func (i *invite) getHeaderIcon() *defaultImage {
	return nil
}
