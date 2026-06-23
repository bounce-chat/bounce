//go:generate gomobile bind -target=android/arm64 -androidapi 26 -o goservice.aar -ldflags=-checklinkname=0 -v github.com/hkparker/bounce/android/service
//go:generate cp goservice.aar ../androidAPK/app/src/main/libs/

package goservice

import (
	"crypto/rand"
	"encoding/base64"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	"github.com/hkparker/bounce/network"

	log "github.com/sirupsen/logrus"
)

var b chat.Engine
var ui *uiBuffer

var timestamp time.Time
var stoppedTime time.Duration

var chDone = make(chan bool)

func StartForegroundService() {
	ui := &uiBuffer{}
	b = chat.Open(ui, &network.TorNetwork{}, getConfigDirectory())
	<-chDone
}

func StopForegroundService() {
	chDone <- true
}

func GetInitialState() string {
	if b == nil {
		return ""
	}
	// TODO: call b.LoadInitialState and serialize it as a string

	return "the initial state from the service"
}

func GetEvents() string {
	if ui == nil {
		return ""
	}
	// TODO: lock and serialize the current event buffer into a string and send it over

	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(bytes)[:16]
}

func Eval(task string) {
	if b == nil {
		return
	}
	// TODO: call the correct function on the chat engine
}

func getConfigDirectory() string {
	var configDirectory string
	if testing.Testing() {
		configDirectory = os.TempDir() + "/bounce-test-" + uuid.New().String()
	} else if runtime.GOOS == "android" {
		// TODO: use /data/data/chat.bounce/ ?
		configDirectory = "/sdcard/Android/data/chat.bounce"
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "configDirectory",
				"error": err.Error(),
			}).Fatal("error getting home directory")
		}
		configDirectory = home + "/.bounce"
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
