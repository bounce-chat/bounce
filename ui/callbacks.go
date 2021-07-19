package ui

import (
	"github.com/hkparker/bounce/chat"
)

func (fyneUI *Fyne) RegisterCallbacks(onMessageSent chat.OutgoingMessageCallback, onAddUserToGroup chat.AddUserToGroupCallback) {
	fyneUI.onMessageSent = onMessageSent
	fyneUI.onAddUserToGroup = onAddUserToGroup
}
