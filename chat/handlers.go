package chat

import (
	log "github.com/sirupsen/logrus"
)

func dispatchMessage(message OutgoingMessage) {
	log.WithFields(log.Fields{
		"destination": message.Destination,
		"text":        message.Text,
	}).Info("UI wants to dispatch a message")
}

func addUserToGroup(threadID, userID string) {
	log.WithFields(log.Fields{
		"thread": threadID,
		"user":   userID,
	}).Info("UI wants to add user to group")
}
