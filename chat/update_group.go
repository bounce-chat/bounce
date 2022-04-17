package chat

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

type updateGroup struct {
	ID          uuid.UUID
	Name        string
	DeleteUsers string
	AddUsers    []user
}

func (b *bounce) addUserToGroup(groupID, userID uuid.UUID) {
	log.WithFields(log.Fields{
		"group": groupID,
		"user":  userID,
	}).Info("UI wants to add user to group")
}

func (b *bounce) renameGroup(groupID uuid.UUID, newName string) {
	log.WithFields(log.Fields{
		"group":    groupID,
		"new_name": newName,
	}).Info("UI wants to rename a group")
}

func (b *bounce) changeGroupNotificationSettings(group uuid.UUID, enabled bool) {
	log.WithFields(log.Fields{
		"thread":                group,
		"notifications_enabled": enabled,
	}).Info("UI wants to chnage notification settings")
}
