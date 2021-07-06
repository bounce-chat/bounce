package chat

import (
	"os"

	log "github.com/sirupsen/logrus"
)

func GetConfigDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "configDirectory",
			"error": err.Error(),
		}).Fatal("error getting home directory")
	}

	return home + "/.bounce"
}
