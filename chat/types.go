package chat

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
