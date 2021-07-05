package main

import (
	//"fyne.io/fyne/v2/app"
	//"fyne.io/fyne/v2/container"
	//"fyne.io/fyne/v2/widget"
	"github.com/hkparker/bounce/network"
	"github.com/hkparker/bounce/chat"
)

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

	//ui ;= ui.NewDesktopUI()
	network := network.NewTorNetwork()
	chat.Start(network)
}
