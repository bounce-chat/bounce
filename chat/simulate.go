package chat

import (
	"time"
)

//TODO: delete this, just for testing UI interactions
func simulate(ui BounceUI) {
	//time.Sleep(3 * time.Second)
	ui.NetworkOnline()
	//time.Sleep(1 * time.Second)
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
	ui.LoadUsers([]User{user1, user2, user3, user4})
	ui.LoadThread(Thread{
		ID:      "001",
		Name:    "Group with Alice and Bob",
		UserIDs: []string{user1.ID, user2.ID},
	})
	//time.Sleep(2 * time.Second)
	ui.LoadThread(Thread{
		ID:      "002",
		Name:    "Group with Bob and Charlie",
		UserIDs: []string{user2.ID, user3.ID},
	})
	//time.Sleep(3 * time.Second)
	ui.LoadThread(Thread{
		ID:      "4",
		Name:    "DM with David",
		UserIDs: []string{user4.ID},
	})
	go func() {
		for i := 0; i < 25; i++ {
			ui.ReceivedMessage(IncomingMessage{ThreadID: "001", UserID: "1", Text: "hello this is from user 1"})
			time.Sleep(1 * time.Second)
		}
	}()
	go func() {
		for i := 0; i < 10; i++ {
			ui.ReceivedMessage(IncomingMessage{ThreadID: "002", UserID: "2", Text: "hello this is from user 2"})
			time.Sleep(5 * time.Second)
		}
	}()

	ui.ReceivedMessage(IncomingMessage{ThreadID: "4", UserID: "4", Text: "hello this is from user 4"})

	/*
		time.Sleep(5 * time.Second)
		fyneUI.NetworkDisconnected()
		time.Sleep(5 * time.Second)
		fyneUI.NetworkLoaded()
	*/
}
