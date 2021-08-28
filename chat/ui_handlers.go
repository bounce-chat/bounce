package chat

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
)

func (bounce *Bounce) sendGroupMessage(message GroupMessage) {
	//bounce.database.Save(message)    // TODO: check for errors
	//bounce.broadcast(newSignedGroupMessage(GroupMessage{
	//	//Source:      "", // TODO: my UUID
	//	Destination: dm.Destination,
	//	Text:        dm.Text,
	//}))
}

func (bounce *Bounce) sendDirectMessage(message DirectMessage) {
	//message.Read = true
	//bounce.database.Save(message) // TODO: check for errors.  Also do actual saves after exported/unexported representations figured out
	bounce.broadcast(&directMessage{
		Source:      bounce.currentUser().ID,
		Destination: message.Destination,
		Text:        message.Text,
	})
	// TODO: broadcast an outoing DM as well as a sync DM?
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

func (bounce *Bounce) setProfile(profileName, deviceName string) error {
	address, err := bounce.network.Address()
	if err != nil {
		return err
	}

	var count int64
	bounce.database.Model(&user{}).Where("profile = ?", true).Count(&count)
	if count > 0 {
		return errors.New("profile already exists on this device")
	}

	return bounce.database.Create(&user{
		ID:      uuid.New(),
		Name:    profileName,
		Profile: true,
		Devices: []device{
			device{
				Name:    deviceName,
				Address: address,
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

	var myProfile user
	err := bounce.database.Model(&user{}).Preload(clause.Associations).Where("profile = ?", true).First(&myProfile).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading contact for export")
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

func (bounce *Bounce) importUser(data []byte) error {
	newUser := profileExport{}
	err := json.Unmarshal(data, &newUser)
	if err != nil {
		return err
	}

	// Make sure the export hasn't expired
	if newUser.Expiration != 0 && newUser.Expiration > time.Now().Unix() {
		return errors.New("file has expired")
	}

	// Make sure we don't already know about any of the devices
	for _, contactDevice := range newUser.Profile.Devices {
		count := int64(0)
		bounce.database.Model(&device{}).Where("address = ?", contactDevice.Address).Count(&count)
		if count > 0 {
			return errors.New("contact contains device that already exists in database")
		}
	}

	// Make sure this contact has a valid device group
	if !newUser.Profile.validDeviceGroup() {
		return errors.New("invalid device group")
	}

	// Save to the database
	return bounce.database.Save(&newUser.Profile).Error
	// TODO: some sort of UI feedback on the secret being accepted on the remote side?
	// As in, bounce only accepts DMs from user's in a shared group or who send an import secret
	// TODO: try to dial right away
}

func (bounce *Bounce) requestUserConnection(userID string) {
	// Make sure there's a connection to the user
	// if there's already a connection, early return and notify the UI that the user is online
	// if not, attempt to make one.  Notify the UI if a connection is made or not
}
