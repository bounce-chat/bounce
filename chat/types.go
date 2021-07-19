package chat

//
// TODO: A description of these types...
//

//
// A bounce address is the public key and dialable address of another
// bounce device on an overlay network.  Not all networks have this
// property, Bounce was designed with I2P or Tor hidden services v3
// in  mind.
//
// Many networks, like libp2p, use a hash of the public key as the
// dialable address.  Bounce would need to be expanded to support
// public key retrival and verification in that design, and may do
// so in the future as needed.
//
type BounceAddress string

type User struct {
	ID   string
	Name string
}

type Thread struct {
	ID      string
	Name    string
	UserIDs []string
}

type IncomingMessage struct {
	ThreadID string
	UserID   string
	Text     string
}

type OutgoingMessage struct {
	Destination string
	Text        string
	// TODO: support images, files, etc
}

type OutgoingMessageCallback func(OutgoingMessage) // TODO: return an error?
type AddUserToGroupCallback func(groupID, userID string)
