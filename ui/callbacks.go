package ui

import (
	"github.com/hkparker/bounce/chat"
)

func (fyneUI *Fyne) SetOnMessageSent(callback func(chat.Message)) {
	fyneUI.onMessageSent = callback
}
