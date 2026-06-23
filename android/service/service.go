//go:generate gomobile bind -target=android/arm64 -androidapi 26 -o goservice.aar -ldflags=-checklinkname=0 -v github.com/hkparker/bounce/android/service
//go:generate cp goservice.aar ../androidAPK/app/src/main/libs/

package goservice

import (
	"crypto/rand"
	"encoding/base64"
	"time"
)

// TODO: var b chat.Bounce

var timestamp time.Time
var stoppedTime time.Duration

var chDone = make(chan bool)

func StartForegroundService() {
	// TODO: b = chat.Open()
	<-chDone
}

func StopForegroundService() {
	chDone <- true
}

func GetInitialState() string {
	return "the initial state from the service"
}

func GetEvents() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return ""
	}
	return base64.URLEncoding.EncodeToString(bytes)[:16]
}
