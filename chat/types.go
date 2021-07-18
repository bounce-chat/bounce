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

type Message struct {
	ThreadID string
	UserID   string
	Text     string
}
