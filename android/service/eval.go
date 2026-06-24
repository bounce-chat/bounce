package goservice

import (
	"encoding/base64"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	log "github.com/sirupsen/logrus"
)

func Eval(rawTask string) string {
	for b == nil {
		log.Warn("waiting for bounce object to be defined before calling")
		time.Sleep(500 * time.Millisecond)
	}

	task, err := base64.StdEncoding.DecodeString(rawTask)
	if err != nil {
		return ""
	}
	cmd := make(map[int][]byte)
	err = msgpack.Unmarshal([]byte(task), &cmd)
	if err != nil {
		// TODO
		return ""
	}

	if len(cmd) == 0 {
		// TODO
		return ""
	}

	funcName, ok := cmd[0]
	if !ok {
		// TODO
		return ""
	}

	switch string(funcName) {
	case "GetInitialState":
		initialState := b.GetInitialState()
		data, err := msgpack.Marshal(&initialState)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error marshalling initial state")
		}
		return base64.StdEncoding.EncodeToString(data)
	case "RequestToAddUser":
		offerBytes, ok := cmd[1]
		if !ok {
			log.Error("no offer bytes")
			return ""
		}
		b.RequestToAddUser(string(offerBytes))
	case "SetProfile":
		name, ok := cmd[1]
		if !ok {
			// TODO
			return ""
		}
		image, ok := cmd[2]
		if !ok {
			// TODO
			return ""
		}
		deviceName, ok := cmd[3]
		if !ok {
			// TODO
			return ""
		}

		err := b.SetProfile(string(name), image, string(deviceName))
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Warn("error setting profile")
		}
	}
	return ""
}
