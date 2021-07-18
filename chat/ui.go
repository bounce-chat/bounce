package chat

type BounceUI interface {
	Build(string)
	Run()
	Quit()
	NetworkLoaded()
	LoadUsers([]User)
	LoadThread(Thread)
	ReceivedMessage(Message)
}
