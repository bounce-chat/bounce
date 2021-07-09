package main

import (
	"github.com/hkparker/bounce/chat"
	"github.com/hkparker/bounce/network"
	"github.com/hkparker/bounce/ui/desktop"
)

func main() {
	chat.Start(
		&network.TorNetwork{},
		&desktop.DesktopUI{},
	)
}
