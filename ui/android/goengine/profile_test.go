package goengine

import (
	"testing"

	"github.com/bounce-chat/bounce/chat"
)

type droppedEvents struct{}

func (droppedEvents) OnEvent(string, string)             {}
func (droppedEvents) OnTyping(string, string, bool)      {}
func (droppedEvents) OnPresence(string, string, bool)    {}
func (droppedEvents) OnProgress(string, string, float64) {}

// The binding learns the local user's ID from whichever event carries the profile, so CurrentUserID works after a
// profile arrives by sync as well as after one is created here, and forgets it when the engine stops.
func TestTheProfileIDIsLearnedFromEitherProfileEvent(t *testing.T) {
	var engine Engine
	adapter := &uiAdapter{sink: droppedEvents{}, profileKnown: engine.setUserID}
	if _, err := engine.CurrentUserID(); err == nil {
		t.Fatal("a fresh binding should not know a user ID")
	}

	created := chat.NewID()
	adapter.ProfileSet(chat.User{ID: created}, chat.Device{})
	if id, err := engine.CurrentUserID(); err != nil || id != created.String() {
		t.Fatalf("after ProfileSet: id=%q err=%v", id, err)
	}

	synced := chat.NewID()
	adapter.SyncDeviceRequestAccepted(chat.User{ID: synced}, nil, false)
	if id, err := engine.CurrentUserID(); err != nil || id != synced.String() {
		t.Fatalf("after SyncDeviceRequestAccepted: id=%q err=%v", id, err)
	}

	engine.Stop()
	if _, err := engine.CurrentUserID(); err == nil {
		t.Fatal("Stop should forget the user ID")
	}
}
