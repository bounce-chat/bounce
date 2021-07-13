package main

import (
	"github.com/hkparker/bounce/chat"
	"github.com/hkparker/bounce/network"
	"github.com/hkparker/bounce/ui"
)

func main() {
	chat.Start(
		&network.TorNetwork{},
		&ui.Fyne{},
	)
}
