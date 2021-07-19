package chat

//
// User interfaces for bounce are achieved by implementing the BounceUI interface.
//

type BounceUI interface {
	//
	// Functions are defined in the order they will be called by the engine.
	Build(configPath string)
	//
	// The following functions are called only during startup, before the UI's Run() function is called, in order
	// to load the current state of the database into the UI
	//
	LoadUsers([]User)
	LoadThread(Thread) // TODO: []Thread
	//LoadChatHistory([]Message)
	//LoadInitialState
	//
	// Callbacks
	//
	RegisterCallbacks(onMessageSent OutgoingMessageCallback, onAddUserToGroup AddUserToGroupCallback)
	//
	// These functions are called as needed by the chat engine
	//
	ReceivedMessage(IncomingMessage)

	NetworkOnline()
	//NetworkDisconnected()

	// Run must display the user interface and block
	Run()
	Quit()
}
