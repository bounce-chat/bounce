package main

import (
	"flag"

	"github.com/bounce-chat/bounce/chat"
	"github.com/bounce-chat/bounce/config"
	"github.com/bounce-chat/bounce/network"
	"github.com/bounce-chat/bounce/ui"
)

func main() {
	encrypted := flag.Bool("encrypted", false, "start an encrypted device")
	flag.Parse()
	if *encrypted {
		chat.StartEncryptedDevice(&network.TorNetwork{}, config.GetEncryptedConfigDirectory())
	} else {
		ui.Main()
	}
}
