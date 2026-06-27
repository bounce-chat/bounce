//go:generate gomobile bind -target=android/arm64 -androidapi 26 -o goservice.aar -ldflags=-checklinkname=0 -v github.com/hkparker/bounce/android/service
//go:generate cp goservice.aar ../apk/app/src/main/libs/

package goservice

import (
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/google/uuid"
	"github.com/hkparker/bounce/chat"
	"github.com/hkparker/bounce/network"

	log "github.com/sirupsen/logrus"
)

var b chat.Engine
var ui *uiBuffer

var chDone = make(chan bool)

type NotificationData struct {
	Title   string
	Content string
}

var notificationChannel = make(chan *NotificationData)

func StartForegroundService() {
	go keepCachePruned()
	ui = &uiBuffer{
		events: []map[int][]byte{},
	}
	b = chat.Open(ui, &network.TorNetwork{}, getConfigDirectory(), handleNotification)
	<-chDone
}

func StopForegroundService() {
	chDone <- true
}

func GetInitialState() string {
	for b == nil {
		log.Warn("waiting for bounce object to be defined before getting initial state")
		time.Sleep(500 * time.Millisecond)
	}

	initialState := b.GetInitialState()
	data, err := msgpack.Marshal(&initialState)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error marshalling initial state")
	}

	return base64.StdEncoding.EncodeToString(data)
}

func GetEvents() string {
	if ui == nil {
		return ""
	}
	return ui.getEvents()
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

func keepCachePruned() {
	pruneCache()
	for range time.NewTicker(1 * time.Hour).C {
		pruneCache()
	}
}

func pruneCache() {
	cutoff := time.Now().Add(-1 * time.Hour)

	err := filepath.WalkDir("/data/data/chat.bounce/cache/", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		if info.ModTime().Before(cutoff) {
			if err := os.Remove(path); err != nil {
				return err
			}
		}

		return nil
	})

	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("error pruning cache directory")
	}
}

func handleNotification(title, content string) {
	notificationChannel <- &NotificationData{
		Title:   title,
		Content: content,
	}
}

func GetNotification() *NotificationData {
	return <-notificationChannel
}
