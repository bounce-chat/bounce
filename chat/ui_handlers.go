package chat

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

func (bounce *Bounce) sendGroupMessage(message GroupMessage) {
}

func (bounce *Bounce) sendDirectMessage(message *DirectMessage) uuid.UUID {
	message.ID = uuid.New()
	message.CreatedAt = time.Now().Unix()
	message.Read = true
	message.Source = bounce.currentUserID()

	err := bounce.database.Create(message).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving direct message to the database")
	}

	bounce.broadcast(message)

	return message.ID
}

func (bounce *Bounce) addUserToGroup(threadID, userID uuid.UUID) {
	log.WithFields(log.Fields{
		"thread": threadID,
		"user":   userID,
	}).Info("UI wants to add user to group")
}

func (bounce *Bounce) renameGroup(threadID uuid.UUID, newName string) {
	log.WithFields(log.Fields{
		"thread":   threadID,
		"new_name": newName,
	}).Info("UI wants to rename a group")
}

func (bounce *Bounce) changeNotificationSettings(threadID uuid.UUID, enabled bool) {
	log.WithFields(log.Fields{
		"thread":                threadID,
		"notifications_enabled": enabled,
	}).Info("UI wants to chnage notification settings")
}

func (bounce *Bounce) setProfile(profileName, deviceName string) (uuid.UUID, error) {
	newID := uuid.New()

	var count int64
	bounce.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count > 0 {
		return newID, errors.New("profile already exists on this device")
	}

	return newID, bounce.database.Create(&user{
		ID:      newID,
		Name:    profileName,
		Profile: true,
		Devices: []device{
			device{
				Name:    deviceName,
				Address: bounce.network.Address(),
			},
		},
	}).Error
}

func (bounce *Bounce) exportContact(name string, expiration int64, oneTime bool) []byte {
	var count int64
	bounce.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count != 1 {
		log.Fatal("no contact exists to export")
	}

	myProfile, exists := bounce.currentUser()
	if !exists {
		log.Fatal("cannot export contact when no profile exists")
	}
	secretBytes := make([]byte, 16)
	rand.Read(secretBytes)
	secret := fmt.Sprintf("%x", secretBytes)

	export := profileExport{
		Name:       name,
		Secret:     secret,
		Expiration: expiration,
		OneTimeUse: oneTime,
		Profile:    myProfile,
	}

	bytes, err := json.Marshal(export)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling contact export")
	}

	err = bounce.database.Save(&export).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error saving contact export")
	}

	return bytes
}

func (bounce *Bounce) importUser(data []byte) (User, error) {
	newUser := profileExport{}
	err := json.Unmarshal(data, &newUser)
	if err != nil {
		return User{}, err
	}

	// Make sure the export hasn't expired
	if newUser.Expiration != 0 && time.Now().Unix() >= newUser.Expiration {
		return User{}, errors.New("file has expired")
	}

	// Make sure we don't already know about any of the devices
	for _, contactDevice := range newUser.Profile.Devices {
		_, exists := bounce.getDeviceFromAddress(contactDevice.Address)
		if exists {
			return User{}, errors.New("contact contains device that already exists in database")
		}
	}

	// Make sure this contact has a valid device group
	if !newUser.Profile.validDeviceGroup() {
		return User{}, errors.New("invalid device group")
	}

	uiUser := User{
		ID:   newUser.Profile.ID,
		Name: newUser.Profile.Name,
	}

	// Save to the database
	return uiUser, bounce.database.Create(&newUser.Profile).Error
	// TODO: some sort of UI feedback on the secret being accepted on the remote side?
	// As in, bounce only accepts DMs from user's in a shared group or who send an import secret
	// TODO: try to dial right away
}

func (bounce *Bounce) requestUserConnection(userID string) {
	// Make sure there's a connection to the user
	// if there's already a connection, early return and notify the UI that the user is online
	// if not, attempt to make one.  Notify the UI if a connection is made or not
}
