package config

import (
	"os"
	"runtime"
	"testing"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var testID = uuid.New()

func GetConfigDirectory() string {
	var configDirectory string
	if testing.Testing() {
		configDirectory = os.TempDir() + "/bounce-test-" + testID.String()
	} else if runtime.GOOS == "android" {
		configDirectory = "/data/data/chat.bounce/bounce"
	} else if runtime.GOOS == "linux" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "configDirectory",
				"error": err.Error(),
			}).Fatal("error getting home directory")
		}
		configDirectory = home + "/.bounce"
	} else {
		home, err := os.UserConfigDir()
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "configDirectory",
				"error": err.Error(),
			}).Fatal("error getting home directory")
		}
		configDirectory = home + "/bounce"
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

func GetEncryptedConfigDirectory() string {
	var configDirectory string
	if testing.Testing() {
		configDirectory = os.TempDir() + "/bounce-encrypted-test-" + testID.String()
	} else if runtime.GOOS == "android" {
		configDirectory = "/data/data/chat.bounce/bounce-encrypted"
	} else if runtime.GOOS == "linux" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "configDirectory",
				"error": err.Error(),
			}).Fatal("error getting home directory")
		}
		configDirectory = home + "/.bounce-encrypted"
	} else {
		home, err := os.UserConfigDir()
		if err != nil {
			log.WithFields(log.Fields{
				"at":    "configDirectory",
				"error": err.Error(),
			}).Fatal("error getting home directory")
		}
		configDirectory = home + "/bounce-encrypted"
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
