package chat

import (
	"encoding/json"
	"errors"

	log "github.com/sirupsen/logrus"
	"gorm.io/gorm/clause"
)

func (bounce *Bounce) sendMessage(message Message) {
	log.WithFields(log.Fields{
		"destination": message.Destination,
		"text":        message.Text,
	}).Info("UI wants to send a message")
	// gossip (takes gossipable interface?)
	//	devices := getDevicePool(destination) // returns []deviceConnection
	//	database.Save(message)
	//	for each device, sent
	//	update database for deliveredTo
	//	UI callbacks for delivery status
}

func (bounce *Bounce) addUserToGroup(threadID, userID string) {
	log.WithFields(log.Fields{
		"thread": threadID,
		"user":   userID,
	}).Info("UI wants to add user to group")
}

func (bounce *Bounce) renameGroup(threadID, newName string) {
	log.WithFields(log.Fields{
		"thread":   threadID,
		"new_name": newName,
	}).Info("UI wants to rename a group")
}

func (bounce *Bounce) changeNotificationSettings(threadID string, enabled bool) {
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
	bounce.database.Model(&profile{}).Count(&count)
	if count > 0 {
		return errors.New("profile already exists on this device")
	}

	return bounce.database.Save(&profile{
		Name: profileName,
		Devices: []device{
			device{
				Name:    deviceName,
				Address: address,
			},
		},
	}).Error
}

// TODO: put this somewhere
type profileExport struct {
	Secret     string
	Expiration int64
	Profile    profile
}

func (bounce *Bounce) exportContact() []byte {
	var count int64
	bounce.database.Model(&profile{}).Count(&count)
	if count != 1 {
		// TODO: fatal
	}

	var myProfile profile
	bounce.database.Preload(clause.Associations).First(&myProfile)
	secret := "secret" // TODO: generate random string and save to database
	// TODO: also set the expiration in the exported struct

	bytes, err := json.Marshal(profileExport{
		Secret:  secret,
		Profile: myProfile,
	})
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error exporting profile")
	}
	return bytes
}

func (bounce *Bounce) importUser(data []byte) error {
	log.Info("user wants to import a contact")
	// Parse and save to DB
	return nil
}

func (bounce *Bounce) requestUserConnection(userID string) {
	// Make sure there's a connection to the user
	// if there's already a connection, early return and notify the UI that the user is online
	// if not, attempt to make one.  Notify the UI if a connection is made or not
}
