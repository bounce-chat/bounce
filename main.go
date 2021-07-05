package main

import (
	"os"

	log "github.com/sirupsen/logrus"

	"github.com/hkparker/bounce/chat"
	"github.com/hkparker/bounce/network"
	"github.com/hkparker/bounce/ui/desktop"
)

func getConfigDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "configDirectory",
			"error": err.Error(),
		}).Fatal("error getting home directory")
	}

	return home + "/.bounce"
}

func main() {
	configDirectory := getConfigDirectory()

	// Build the user interface
	ui := desktop.NewDesktopUI()

	// Initialize the network that will carry Bounce
	network := network.NewTorNetwork(configDirectory)

	// Start the chat engine using the chosen network and user interface
	err := chat.Start(network, ui)
	if err != nil {
		log.WithFields(log.Fields{
			"at":    "main",
			"error": err.Error(),
		}).Error("bounce stopped with error")
	} else {
		log.WithFields(log.Fields{
			"at": "main",
		}).Info("bounce stopped")
	}
}
