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
	RegisterCallbacks(UICallbacks)
	// Load the initial state
	LoadInitialState(InitialState)

	//
	// These functions control the user interface lifecycle
	//

	// Run displays the user interface and blocks.  A network loading message should be displayed first until NetworkOnline() is called.
	Run()
	// Application is closing due to a fatal error, show down the user interface
	Quit()

	//
	// The following functions can be called at any time
	//

	// The network is ready
	NetworkOnline()
	// Network connection has been lost, go back to displaying a loading message, blocking user interaction
	NetworkDisconnected()

	// New chat message to display in a thread

	ReceivedMessage(Message) // TODO: should be specific to threads (DM vs group)
	//RenameGroup()

	// TODO:  Just testing, to be removed
	NewThread(Thread)
	NewUser(string, string)
}
