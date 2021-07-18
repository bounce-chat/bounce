package chat

import (
	log "github.com/sirupsen/logrus"
)

func dispatchMessage(message Message) {
	log.WithFields(log.Fields{
		"thread": message.ThreadID,
		"text":   message.Text,
	}).Info("UI wants to dispatch a message")
}
