package chat

type BounceUI interface {
	Build(string)
	Run()
	Quit()
	NetworkLoaded()
	//NetworkDisconnected()

	//
	// Callbacks
	//
	SetOnMessageSent(func(Message)) // TODO: return an error?

	//
	// The following functions are called only during startup, before the UI's Run() function is called, in order
	// to load the current state of the database into the UI
	//
	LoadUsers([]User)
	LoadThread(Thread) // TODO: []Thread
	//LoadChatHistory([]Message)
	//
	// These functions are called as needed by the chat engine
	//
	ReceivedMessage(Message)
}
