package main

import (
	//"fyne.io/fyne/v2/app"
	//"fyne.io/fyne/v2/container"
	//"fyne.io/fyne/v2/widget"
	log "github.com/sirupsen/logrus"
	"os"

	"github.com/hkparker/bounce/chat"
	"github.com/hkparker/bounce/network"
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
	/*
		a := app.New()
		w := a.NewWindow("Hello")

		hello := widget.NewLabel("Hello Fyne!")
		w.SetContent(container.NewVBox(
			hello,
			widget.NewButton("Hi!", func() {
				hello.SetText("Welcome :)")
			}),
		))

		w.ShowAndRun()
	*/
	configDirectory := getConfigDirectory()

	//ui ;= ui.NewDesktopUI()
	network := network.NewTorNetwork(configDirectory)
	chat.Start(network)
}
