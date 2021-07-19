package chat

//
// User interfaces for bounce are achieved by implementing the BounceUI interface.
//

type BounceUI interface {
	//
	// During initial startup, the following functions are called in order to build the interface
	//

	// Create user interface objects
	Build(configPath string)
	// Define callbacks the interface will use to communicate with the chat ending
	RegisterCallbacks(onMessageSent OutgoingMessageCallback, onAddUserToGroup AddUserToGroupCallback)
	// Load the initial state
	LoadUsers([]User)
	LoadThread(Thread) // TODO: []Thread
	//LoadChatHistory([]Message)
	//LoadInitialState

	//
	// The following functions can be called at any time
	//

	// The network is ready
	NetworkOnline()
	// Network connection has been lost, go back to displaying a loading message, blocking user interaction
	//NetworkDisconnected()

	// New chat message to display in a thread
	ReceivedMessage(IncomingMessage)

	// Run displays the user interface and blocks.  A network loading message should be displayed first until NetworkOnline() is called.
	Run()
	// Application is closing due to a fatal error, show down the user interface
	Quit()
}
