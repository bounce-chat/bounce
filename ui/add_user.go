package ui

import (
	"github.com/hkparker/bounce/chat"
)

func (fyneUI *Fyne) AddUserRequestRejected() {
	// TODO: if there's an open pending friend request, close it and display an error
}

func (fyneUI *Fyne) FriendAdded(user chat.User) {
	// TODO: close any open add friend dialog, display that this friend has been added
}
