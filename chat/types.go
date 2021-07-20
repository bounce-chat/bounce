package chat

//
// Common types passed to the UI or network implementations
//

//
// A bounce address is the public key and dialable address of another
// bounce device on an overlay network.  Not all networks have this
// property, Bounce was designed with I2P or Tor hidden services v3
// in  mind.  In networks where the dialable address is a hash of a
// public key, like libp2p, the network implementation will need to
// internally retrieve, store, and use the public key for the address.
//
type BounceAddress string

type InitialState struct {
	Profile          User
	Users            []User
	Threads          []Thread
	ReceivedMessages []IncomingMessage
	SentMessages     []OutgoingMessage
}

type User struct {
	ID    string
	Name  string
	Image []byte
}

type Thread struct {
	ID      string
	Name    string
	UserIDs []string
}

type IncomingMessage struct {
	ThreadID  string
	CreatedAt int64
	UserID    string
	Text      string
}

type OutgoingMessage struct {
	CreatedAt   int64
	Destination string
	Text        string
	// TODO: support images, files, etc
}

//
// The chat engine will provide these callbacks to a user interface
//
type Callbacks struct {
	// The user wants to send a message
	SendMessage SendMessageCallback
	// The user wants to add another user to a group
	AddUserToGroup AddUserToGroupCallback
	// The user wants to rename a group
	RenameGroup RenameGroupCallback
	// The user wants to change the notification settings for a group on all their devices
	ChangeNotificationSettings ChangeNotificationSettingsCallback

	//Unimplemented:
	//MessageRead()
}

type SendMessageCallback func(OutgoingMessage) // TODO: return an error?
type AddUserToGroupCallback func(groupID, userID string)
type RenameGroupCallback func(groupID, newName string)
type ChangeNotificationSettingsCallback func(groupID string, notificationEnabled bool)
