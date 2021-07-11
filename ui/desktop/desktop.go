package desktop

import (
	// Fyne
	/*
		"fyne.io/fyne/v2"
		"fyne.io/fyne/v2/app"
		"fyne.io/fyne/v2/container"
		"fyne.io/fyne/v2/widget"
		log "github.com/sirupsen/logrus"
	*/

	// GTK
	"github.com/gotk3/gotk3/gtk"
	log "github.com/sirupsen/logrus"
	// QML
	/*
		"os"

		"github.com/therecipe/qt/core"
		"github.com/therecipe/qt/gui"
		"github.com/therecipe/qt/qml"
		"github.com/therecipe/qt/quickcontrols2"
	*/// QT
	/*
		"fmt"
		"os"

		"github.com/therecipe/qt/core"
		"github.com/therecipe/qt/widgets"
	*/)

type DesktopUI struct {
	//app fyne.App
	//app *widgets.QApplication
}

func (desktopUI *DesktopUI) Build(configDirectory string) {
	// Fyne
	/*
		a := app.New()
		desktopUI.app = a
		w := a.NewWindow("Bounce")
		w.SetMaster()
		w.SetCloseIntercept(func() {
			log.WithFields(log.Fields{
				"at": "desktop.DesktopUI.Build",
			}).Info("window close button hit, shutting down")
			//w.Close()
			//a.Quit()
			desktopUI.Quit()
		})

		threads := container.NewVBox(
			widget.NewButton("Chat 1", func() {}),
			widget.NewButton("Chat 2", func() {}),
		)
		w.SetContent(container.NewHSplit(
			threads,
			container.NewVSplit(
				widget.NewMultiLineEntry(),
				widget.NewMultiLineEntry(),
			),
		))
		w.Show()
	*/

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
	table, _ := gtk.TextTagTableNew()
	chatHistoryBuffer, _ := gtk.TextBufferNew(table)
	chatHistoryBuffer.InsertAtCursor("some chat history")
	chatHistory, _ := gtk.TextViewNewWithBuffer(chatHistoryBuffer)
	chatHistory.SetSizeRequest(300, 600)
	chatHistory.SetEditable(false)
	activeChat.Add(chatHistory)
	activeChat.Add(activeChatEntry)
	layout.Add(chats)
	layout.Add(activeChat)

	// Add the label to the window.
	win.Add(layout)

	// Set the default window size.
	win.SetDefaultSize(800, 600)

	// Recursively show all widgets contained in this window.
	win.ShowAll()

	// QML
	/*
		// enable high dpi scaling
		// useful for devices with high pixel density displays
		// such as smartphones, retina displays, ...
		core.QCoreApplication_SetAttribute(core.Qt__AA_EnableHighDpiScaling, true)

		// needs to be called once before you can start using QML
		gui.NewQGuiApplication(len(os.Args), os.Args)

		// use the material style
		// the other inbuild styles are:
		// Default, Fusion, Imagine, Universal
		quickcontrols2.QQuickStyle_SetStyle("Material")

		// create the qml application engine
		engine := qml.NewQQmlApplicationEngine(nil)

		// load the embedded qml file
		// created by either qtrcc or qtdeploy
		//engine.Load(core.NewQUrl3("qrc:/qml/main.qml", 0))
		// you can also load a local file like this instead:
		engine.Load(core.QUrl_FromLocalFile("./ui/desktop/qml/main.qml"))
	*/

	/*
		app := widgets.NewQApplication(len(os.Args), os.Args)

		window := widgets.NewQMainWindow(nil, 0)
		window.SetMinimumSize2(250, 200)
		window.SetWindowTitle("listview Example")

		widget := widgets.NewQWidget(nil, 0)
		widget.SetLayout(widgets.NewQVBoxLayout())
		window.SetCentralWidget(widget)

		listview := widgets.NewQListView(nil)
		//model := NewCustomListModel(nil)
		model := &CustomListModel{}
		listview.SetModel(model)
		widget.Layout().AddWidget(listview)

		remove := widgets.NewQPushButton2("remove last item", nil)
		remove.ConnectClicked(func(bool) {
			model.Remove()
		})
		widget.Layout().AddWidget(remove)

		add := widgets.NewQPushButton2("add new item", nil)
		add.ConnectClicked(func(bool) {
			model.Add(ListItem{"john", "doe"})
		})
		widget.Layout().AddWidget(add)

		edit := widgets.NewQPushButton2("edit last item", nil)
		edit.ConnectClicked(func(bool) {
			model.Edit("bob", "omb")
		})
		widget.Layout().AddWidget(edit)

		window.Show()

		desktopUI.app = app
	*/
}

func (desktopUI *DesktopUI) Run() {
	//desktopUI.app.Run()

	//GTK
	gtk.Main()

	// start the main Qt event loop
	// and block until app.Exit() is called
	// or the window is closed by the user
	//gui.QGuiApplication_Exec()

	//desktopUI.app.Exec()
}

func (desktopUI *DesktopUI) Quit() {
	//desktopUI.app.Quit()
	//gui.Exit(0)
	gtk.MainQuit()
}

/*
type ListItem struct {
	firstName string
	lastName  string
}

type CustomListModel struct {
	core.QAbstractListModel

	_ func() `constructor:"init"`

	_ func()                                  `signal:"remove,auto"`
	_ func(item ListItem)                     `signal:"add,auto"`
	_ func(firstName string, lastName string) `signal:"edit,auto"`

	modelData []ListItem
}

func (m *CustomListModel) init() {
	m.modelData = []ListItem{{"john", "doe"}, {"john", "bob"}}

	m.ConnectRowCount(m.rowCount)
	m.ConnectData(m.data)
}

func (m *CustomListModel) rowCount(*core.QModelIndex) int {
	return len(m.modelData)
}

func (m *CustomListModel) data(index *core.QModelIndex, role int) *core.QVariant {
	if role != int(core.Qt__DisplayRole) {
		return core.NewQVariant()
	}

	item := m.modelData[index.Row()]
	return core.NewQVariant1(fmt.Sprintf("%v %v", item.firstName, item.lastName))
}

func (m *CustomListModel) Remove() {
	if len(m.modelData) == 0 {
		return
	}
	m.BeginRemoveRows(core.NewQModelIndex(), len(m.modelData)-1, len(m.modelData)-1)
	m.modelData = m.modelData[:len(m.modelData)-1]
	m.EndRemoveRows()
}

func (m *CustomListModel) Add(item ListItem) {
	m.BeginInsertRows(core.NewQModelIndex(), len(m.modelData), len(m.modelData))
	m.modelData = append(m.modelData, item)
	m.EndInsertRows()
}

func (m *CustomListModel) Edit(firstName string, lastName string) {
	if len(m.modelData) == 0 {
		return
	}
	m.modelData[len(m.modelData)-1] = ListItem{firstName, lastName}
	m.DataChanged(m.Index(len(m.modelData)-1, 0, core.NewQModelIndex()), m.Index(len(m.modelData)-1, 0, core.NewQModelIndex()), []int{int(core.Qt__DisplayRole)})
}

*/
