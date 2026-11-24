package main

import (
	"flag"

	"github.com/bounce-chat/bounce/chat"
	"github.com/bounce-chat/bounce/network/tor"
	"github.com/bounce-chat/bounce/platform"
	"github.com/bounce-chat/bounce/ui/fyneui"
)

func main() {
	encrypted := flag.Bool("encrypted", false, "start an encrypted device")
	flag.Parse()
	if *encrypted {
		chat.StartEncryptedDevice(&tor.Network{}, platform.GetEncrypted())
	} else {
		fyneui.Main()
	}
}
