package desktop

import (
	"github.com/gotk3/gotk3/gtk"
	log "github.com/sirupsen/logrus"
)

type DesktopUI struct {
}

func (desktopUI *DesktopUI) Build(configDirectory string) {
	// Initialize GTK without parsing any command line arguments.
	gtk.Init(nil)

	// Create a new toplevel window, set its title, and connect it to the
	// "destroy" signal to exit the GTK main loop when it is destroyed.
	win, err := gtk.WindowNew(gtk.WINDOW_TOPLEVEL)
	if err != nil {
		log.Fatal("Unable to create window:", err)
	}
	win.SetTitle("Bounce")
	win.Connect("destroy", func() {
		gtk.MainQuit()
	})

	// Create a new label widget to show in the window.
	l, err := gtk.LabelNew("Hello, gotk3!")
	if err != nil {
		log.Fatal("Unable to create label:", err)
	}

	layout, _ := gtk.PanedNew(gtk.ORIENTATION_HORIZONTAL)
	layout.SetSizeRequest(1000, 700)
	l.SetSizeRequest(200, 10)
	chats, _ := gtk.PanedNew(gtk.ORIENTATION_VERTICAL)
	chats.Add(l)
	activeChat, _ := gtk.PanedNew(gtk.ORIENTATION_VERTICAL)
	activeChatEntry, _ := gtk.EntryNew()
	activeChatEntry.SetSizeRequest(300, 100)
	activeChat.Pack1(getCurrentChatHistory(), true, false) // resize, shrink
	activeChat.Pack2(activeChatEntry, true, false)
	layout.Pack1(chats, true, false)
	layout.Pack2(activeChat, true, false)

	// Add the label to the window.
	win.Add(layout)

	// Set the default window size.
	win.SetDefaultSize(800, 600)

	// Recursively show all widgets contained in this window.
	win.ShowAll()
}

func (desktopUI *DesktopUI) Run() {
	gtk.Main()
}

func (desktopUI *DesktopUI) Quit() {
	gtk.MainQuit()
}

//
// GTK views
//

func getCurrentChatHistory() *gtk.ScrolledWindow { // box should be wrapped in scrollable
	/*
		sw = gtk_scrolled_window_new (NULL, NULL);
		gtk_scrolled_window_set_policy (
			GTK_SCROLLED_WINDOW (sw),
			GTK_POLICY_AUTOMATIC,
			GTK_POLICY_AUTOMATIC);
	*/
	scrollWindow, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		log.Fatal(err.Error())
	}
	//scrollWindow.SetMinContentHeight(600)
	//scrollWindow.SetMinContentWidth(300)
	scrollWindow.SetHExpand(true)
	scrollWindow.SetVExpand(true)
	currentChatHistory, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	if err != nil {
		log.Fatal(err.Error())
	}

	table, _ := gtk.TextTagTableNew()
	chatHistoryBuffer, _ := gtk.TextBufferNew(table)
	for i := 0; i < 100; i++ {
		chatHistoryBuffer.InsertMarkup(chatHistoryBuffer.GetEndIter(), "<b><span foreground=\"#0000FF\">username</span></b>: sent this message\n")
	}
	chatHistory, _ := gtk.TextViewNewWithBuffer(chatHistoryBuffer)
	//chatHistory.SetSizeRequest(300, 600)
	chatHistory.SetEditable(false)
	chatHistory.SetHExpand(true)
	chatHistory.SetVExpand(true)

	currentChatHistory.Add(chatHistory)
	currentChatHistory.SetHExpand(true)
	currentChatHistory.SetVExpand(true)
	scrollWindow.Add(currentChatHistory)
	return scrollWindow
}
