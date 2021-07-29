package chat

import (
	"time"
)

//TODO: delete this, just for testing UI interactions
func simulate(ui BounceUI) {
	// "Loaded from the database"
	user1 := User{
		ID:   "1",
		Name: "Alice",
	}
	user2 := User{
		ID:   "2",
		Name: "Bob",
	}
	user3 := User{
		ID:   "3",
		Name: "Charlie",
	}
	user4 := User{
		ID:   "4",
		Name: "David",
	}
	thread1 := Thread{
		ID:      "001",
		Name:    "Group with Alice and Bob",
		UserIDs: []string{user1.ID, user2.ID},
	}
	thread2 := Thread{
		ID:      "002",
		Name:    "Group with Bob and Charlie",
		UserIDs: []string{user2.ID, user3.ID},
	}
	thread3 := Thread{
		ID:      "4",
		Name:    "Group with David",
		UserIDs: []string{user4.ID},
	}

	//time.Sleep(3 * time.Second)
	ui.NetworkOnline()
	//time.Sleep(3 * time.Second)

	ui.NewUser(user1.ID, user1.Name)
	ui.NewUser(user2.ID, user2.Name)
	ui.NewUser(user3.ID, user3.Name)
	ui.NewUser(user4.ID, user4.Name)
	ui.NewThread(thread1)
	ui.NewThread(thread2)
	ui.NewThread(thread3)

	go func() {
		for i := 0; i < 25; i++ {
			ui.ReceivedMessage(Message{Destination: thread1.ID, Source: user1.ID, Text: "hello this is from user 1"})
			time.Sleep(1 * time.Second)
		}
	}()
	go func() {
		for i := 0; i < 10; i++ {
			ui.ReceivedMessage(Message{Destination: thread2.ID, Source: user2.ID, Text: "hello this is from user 2"})
			time.Sleep(5 * time.Second)
		}
	}()

	ui.ReceivedMessage(Message{Destination: thread3.ID, Source: user4.ID, Text: "hello this is from user 4.  this is a long message that is certainly going to wrap so that the bubble view can be tested."})

	//time.Sleep(5 * time.Second)
	//ui.NetworkDisconnected()
	//time.Sleep(5 * time.Second)
	//ui.NetworkOnline()
}
