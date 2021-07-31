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
	ProfileSet bool // *User
	//Devices  []Device
	Users    []User
	Threads  []Thread
	Messages []Message // TODO: break this into each type of message once possible.  Right now they all need to be sorted by timestamp
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

type IncomingThreadMessage struct {
	ID          string
	CreatedAt   int64
	Read        bool
	Author      string // User UUID
	Destination string // Group ID
	Text        string
}

type OutgoingThreadMessage struct {
}

type IncomingDirectMessage struct {
}

type OutgoingDirectMessage struct {
}

type Device struct {
	// SQL relation to user who owns it
	Address BounceAddress
}

//
// The chat engine will provide these callbacks to a user interface
//
type NetworkCallbacks struct {
	NetworkOffline func()
}

//
// The chat engine will provide these callbacks to a user interface
//
type UICallbacks struct {
	SendMessage                SendMessageCallback
	AddUserToGroup             AddUserToGroupCallback
	RenameGroup                RenameGroupCallback
	ChangeNotificationSettings ChangeNotificationSettingsCallback
	SetProfile                 SetProfileCallback

	//Unimplemented:
	//MessageRead()
}

// The user wants to send a message
type SendMessageCallback func(Message) // TODO: return an error?

// The user wants to add another user to a group
type AddUserToGroupCallback func(groupID, userID string)

// The user wants to rename a group
type RenameGroupCallback func(groupID, newName string)

// The user wants to change the notification settings for a group on all their devices
type ChangeNotificationSettingsCallback func(groupID string, notificationEnabled bool)

// Setup a new profile on a fresh install
type SetProfileCallback func(profileName, deviceName string) error

//
// TODO: to be deleted once I figure out loading messages in order during initial state loading.  Or maybe not?
//

type Message struct {
	ID          string
	CreatedAt   int64
	Read        bool
	Source      string // Always a user's UUID
	Destination string // a user UUID or a group UUID
	Text        string
}
