package main

import (
	//"net/http"
	//_ "net/http/pprof"

	"flag"

	"github.com/bounce-chat/bounce/chat"
	"github.com/bounce-chat/bounce/config"
	"github.com/bounce-chat/bounce/network"
	"github.com/bounce-chat/bounce/ui"
)

func main() {
	//go func() {
	//	log.Println(http.ListenAndServe("localhost:6060", nil))
	//}()

	encrypted := flag.Bool("encrypted", false, "start an encrypted device")
	flag.Parse()
	if *encrypted {
		chat.StartEncryptedDevice(&network.TorNetwork{}, config.GetEncryptedConfigDirectory())
	} else {
		ui.Main()
	}
}
