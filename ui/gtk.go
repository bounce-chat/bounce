package ui

/*
import (
	"time"

	"github.com/gotk3/gotk3/gtk"
	log "github.com/sirupsen/logrus"
)

type GTK struct {
	demobuffer *gtk.TextBuffer
}

func (desktopUI *GTK) Build(configDirectory string) {
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
	activeChatEntry.SetSizeRequest(300, 10)
	activeChat.Pack1(desktopUI.getCurrentChatHistory(), true, false) // resize, shrink
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

func (desktopUI *GTK) Run() {
	go func() {
		for i := 0; i < 100; i++ {
			desktopUI.demobuffer.InsertMarkup(desktopUI.demobuffer.GetEndIter(), "<b><span foreground=\"#0000FF\">username</span></b>: sent this message\n")
			time.Sleep(100 * time.Millisecond)
			//scroll to the bottom
			//adjustment := scrollWindow.GetVAdjustment()
			//adjustment.SetValue(adjustment.GetUpper())
		}
	}()

	gtk.Main()
}

func (desktopUI *GTK) Quit() {
	gtk.MainQuit()
}

//
// GTK views
//

func (desktopUI *GTK) getCurrentChatHistory() *gtk.ScrolledWindow { // box should be wrapped in scrollable
	//	sw = gtk_scrolled_window_new (NULL, NULL);
	//	gtk_scrolled_window_set_policy (
	//		GTK_SCROLLED_WINDOW (sw),
	//		GTK_POLICY_AUTOMATIC,
	//		GTK_POLICY_AUTOMATIC);

	scrollWindow, err := gtk.ScrolledWindowNew(nil, nil)
	if err != nil {
		log.Fatal(err.Error())
	}
	//scrollWindow.SetMinContentHeight(600)
	//scrollWindow.SetMinContentWidth(300)
	scrollWindow.SetHExpand(true)
	scrollWindow.SetVExpand(true)
	scrollWindow.SetSizeRequest(600, 700)
	if err != nil {
		log.Fatal(err.Error())
	}

	table, _ := gtk.TextTagTableNew()
	chatHistoryBuffer, _ := gtk.TextBufferNew(table)
	desktopUI.demobuffer = chatHistoryBuffer

	chatHistory, _ := gtk.TextViewNewWithBuffer(chatHistoryBuffer)
	chatHistory.SetEditable(false)
	chatHistory.SetHExpand(true)
	chatHistory.SetVExpand(true)
	//chatHistory.Connect("size-allocate", func(ch *gtk.TextView) {
	//	ch.ScrollToIter(chatHistoryBuffer.GetEndIter(), 0, false, 0, 0)
	//})

	//currentChatHistory, err := gtk.BoxNew(gtk.ORIENTATION_VERTICAL, 0)
	//currentChatHistory.Add(chatHistory)
	//currentChatHistory.SetHExpand(true)
	//currentChatHistory.SetVExpand(true)
	chatHistory.SetHExpand(true)
	chatHistory.SetVExpand(true)
	//scrollWindow.Add(currentChatHistory)
	scrollWindow.Add(chatHistory)
	return scrollWindow
}
*/
