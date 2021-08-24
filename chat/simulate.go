package chat

import (
	"time"
)

func (bounce *Bounce) seedTestDatabase() {
	var userCount int64
	bounce.database.Model(&user{}).Count(&userCount)
	if userCount < 4 {
		bounce.database.Save(&user{
			Name: "Alice",
		})
		bounce.database.Save(&user{
			Name: "Bob",
		})
		bounce.database.Save(&user{
			Name: "Charlie",
		})
		bounce.database.Save(&user{
			Name: "Dave",
		})
	}
}

//TODO: delete this, just for testing UI interactions
func simulate(ui BounceUI) {
	// "Loaded from the database"
	user1 := User{
		ID:   "1",
		Name: "Alice2",
	}
	user2 := User{
		ID:   "2",
		Name: "Bob2",
	}
	user3 := User{
		ID:   "3",
		Name: "Charlie2",
	}
	user4 := User{
		ID:   "4",
		Name: "David2",
	}
	thread1 := Group{
		ID:      "001",
		Name:    "Group with Alice and Bob",
		UserIDs: []string{user1.ID, user2.ID},
	}
	thread2 := Group{
		ID:      "002",
		Name:    "Group with Bob and Charlie",
		UserIDs: []string{user2.ID, user3.ID},
	}

	//time.Sleep(3 * time.Second)
	ui.NetworkOnline()
	//time.Sleep(3 * time.Second)

	ui.UserImported(user1)
	ui.UserImported(user2)
	ui.UserImported(user3)
	ui.UserImported(user4)
	ui.NewGroupChat(thread1)
	ui.NewGroupChat(thread2)

	go func() {
		for i := 0; i < 25; i++ {
			ui.ReceivedGroupMessage(GroupMessage{Destination: thread1.ID, Source: user1.ID, Text: "hello this is from user 1"})
			time.Sleep(1 * time.Second)
		}
	}()
	go func() {
		for i := 0; i < 10; i++ {
			ui.ReceivedGroupMessage(GroupMessage{Destination: thread2.ID, Source: user2.ID, Text: "hello this is from user 2"})
			time.Sleep(5 * time.Second)
		}
	}()

	ui.ReceivedDirectMessage(DirectMessage{Destination: "", Source: user4.ID, Text: "hello this is from user 4.  this is a long message that is certainly going to wrap so that the bubble view can be tested."})

	//time.Sleep(5 * time.Second)
	//ui.NetworkDisconnected()
	//time.Sleep(5 * time.Second)
	//ui.NetworkOnline()
}
