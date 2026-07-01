package main

import (
	//"net/http"
	//_ "net/http/pprof"

	"flag"
	"os"
	"runtime"
	"testing"

	"github.com/google/uuid"
	"github.com/bounce-chat/bounce/chat"
	"github.com/bounce-chat/bounce/network"
	"github.com/bounce-chat/bounce/ui"
	log "github.com/sirupsen/logrus"
)

func main() {
	//go func() {
	//	log.Println(http.ListenAndServe("localhost:6060", nil))
	//}()

	encrypted := flag.Bool("encrypted", false, "start an encrypted device")
	flag.Parse()
	if *encrypted {
		chat.StartEncryptedDevice(&network.TorNetwork{}, getEncryptedConfigDirectory())
	} else {
		ui.Main()
	}
}

func getEncryptedConfigDirectory() string {
	var configDirectory string
	if testing.Testing() {
		configDirectory = os.TempDir() + "/bounce-encrypted-test-" + uuid.New().String()
	} else if runtime.GOOS == "android" {
		// TODO: use /data/data/chat.bounce/ ?
		configDirectory = "/sdcard/Android/data/chat.bounce.encrypted"
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "configDirectory",
				"error": err.Error(),
			}).Fatal("error getting home directory")
		}
		configDirectory = home + "/.bounce-encrypted"
	}

	err := os.MkdirAll(configDirectory+"/blobs/", 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"path":  configDirectory,
			"error": err.Error(),
		}).Fatal("error creating config directory")
	}

	return configDirectory
}
