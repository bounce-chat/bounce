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

func renameGroup(threadID, newName string) {
	log.WithFields(log.Fields{
		"thread":   threadID,
		"new_name": newName,
	}).Info("UI wants to rename a group")
}

func changeNotificationSettings(threadID string, enabled bool) {
	log.WithFields(log.Fields{
		"thread":                threadID,
		"notifications_enabled": enabled,
	}).Info("UI wants to chnage notification settings")
}
