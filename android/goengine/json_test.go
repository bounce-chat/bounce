package goengine

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/bounce-chat/bounce/chat"
	"github.com/google/uuid"
)

// The Kotlin layer parses every engine event with kotlinx.serialization, so the
// JSON encoding of chat's DTOs is a wire format now. These tests pin the two
// properties Kotlin depends on and that are easy to break from the Go side:
// UUIDs render as canonical strings (not 16-element byte arrays), and maps keyed
// by uuid.UUID render as string-keyed objects.

func TestUUIDsMarshalAsCanonicalStrings(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	b, err := json.Marshal(chat.DirectMessage{ID: id, Author: id, Thread: id})
	if err != nil {
		t.Fatalf("marshalling direct message: %v", err)
	}

	got := string(b)
	if !strings.Contains(got, `"550e8400-e29b-41d4-a716-446655440000"`) {
		t.Fatalf("uuid did not marshal as a canonical string, got %s", got)
	}
	// A [16]byte that lost its TextMarshaler would come out as a JSON array.
	if strings.Contains(got, `"ID":[`) {
		t.Fatalf("uuid marshalled as a byte array, Kotlin cannot parse this: %s", got)
	}
}

func TestBulkUpdateMapsKeyOnUUIDStrings(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	bu := chat.BulkUpdate{
		Source:         id,
		Seen:           []uuid.UUID{id},
		GroupMessages:  map[uuid.UUID][]chat.GroupMessage{id: {{ID: id, Text: "hi"}}},
		DirectMessages: map[uuid.UUID][]chat.DirectMessage{id: {{ID: id, Text: "hi"}}},
		ReadReceipts:   map[uuid.UUID][]chat.ReadReceipt{id: {{ID: id}}},
	}

	b, err := json.Marshal(bu)
	if err != nil {
		// encoding/json only accepts non-string map keys when the key type
		// implements TextMarshaler. uuid.UUID does; this guards a regression.
		t.Fatalf("marshalling bulk update: %v", err)
	}

	var round chat.BulkUpdate
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshalling bulk update: %v", err)
	}
	if len(round.GroupMessages[id]) != 1 || round.GroupMessages[id][0].Text != "hi" {
		t.Fatalf("bulk update did not round trip: %+v", round)
	}
}

// Every DTO that crosses to Kotlin must survive a marshal/unmarshal round trip.
// A type that fails here is a type Kotlin will silently receive as garbage.
func TestEveryUIDTORoundTrips(t *testing.T) {
	id := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")

	cases := []struct {
		name string
		val  any
		into any
	}{
		{"User", chat.User{ID: id, Name: "Alice", Images: []uuid.UUID{id}}, &chat.User{}},
		{"Device", chat.Device{ID: id, Name: "Phone", Address: "abc"}, &chat.Device{}},
		{"Settings", chat.Settings{DefaultGroupRetention: 42}, &chat.Settings{}},
		{"DMState", chat.DMState{Open: true, Retention: 7}, &chat.DMState{}},
		{"DirectMessage", chat.DirectMessage{ID: id, Text: "hello"}, &chat.DirectMessage{}},
		{"GroupMessage", chat.GroupMessage{ID: id, Text: "hello"}, &chat.GroupMessage{}},
		{"Group", chat.Group{ID: id, Name: "Friends", Admins: []uuid.UUID{id}}, &chat.Group{}},
		{"NewGroup", chat.NewGroup{Name: "Friends", InitialInvites: []uuid.UUID{id}}, &chat.NewGroup{}},
		{"ImageAttachment", chat.ImageAttachment{ID: id, Width: 4, Height: 2}, &chat.ImageAttachment{}},
		{"FileAttachment", chat.FileAttachment{ID: id, Name: "a.pdf"}, &chat.FileAttachment{}},
		{"ReadReceipt", chat.ReadReceipt{ID: id, Actor: id, Target: id}, &chat.ReadReceipt{}},
		{"Draft", chat.Draft{Thread: id, Text: "wip"}, &chat.Draft{}},
		{"FileProgress", chat.FileProgress{ID: id, Progress: 0.5}, &chat.FileProgress{}},
		{"UpdateDMRetention", chat.UpdateDMRetention{ID: id, Retention: 1}, &chat.UpdateDMRetention{}},
		{"UpdateDMClearHistory", chat.UpdateDMClearHistory{ID: id}, &chat.UpdateDMClearHistory{}},
		{"UpdateDMSetAlias", chat.UpdateDMSetAlias{ID: id, Alias: "A"}, &chat.UpdateDMSetAlias{}},
		{"UpdateGroupName", chat.UpdateGroupName{ID: id, Name: "N"}, &chat.UpdateGroupName{}},
		{"UpdateGroupRetention", chat.UpdateGroupRetention{ID: id}, &chat.UpdateGroupRetention{}},
		{"UpdateGroupInviteUser", chat.UpdateGroupInviteUser{ID: id}, &chat.UpdateGroupInviteUser{}},
		{"UpdateGroupRemoveUser", chat.UpdateGroupRemoveUser{ID: id}, &chat.UpdateGroupRemoveUser{}},
		{"UpdateGroupClearHistory", chat.UpdateGroupClearHistory{ID: id}, &chat.UpdateGroupClearHistory{}},
		{"UpdateGroupAdminPromoted", chat.UpdateGroupAdminPromoted{ID: id}, &chat.UpdateGroupAdminPromoted{}},
		{"UpdateGroupAdminDemoted", chat.UpdateGroupAdminDemoted{ID: id}, &chat.UpdateGroupAdminDemoted{}},
		{"UpdateGroupInviteRevoked", chat.UpdateGroupInviteRevoked{ID: id}, &chat.UpdateGroupInviteRevoked{}},
		{"UpdateGroupInviteAccepted", chat.UpdateGroupInviteAccepted{ID: id}, &chat.UpdateGroupInviteAccepted{}},
		{"UpdateGroupInviteRejected", chat.UpdateGroupInviteRejected{ID: id}, &chat.UpdateGroupInviteRejected{}},
		{"UserBlockedGroup", chat.UserBlockedGroup{ID: id}, &chat.UserBlockedGroup{}},
		{"RemovedFromGroup", chat.RemovedFromGroup{Group: id, Actor: id}, &chat.RemovedFromGroup{}},
		{"GroupDeleted", chat.GroupDeleted{Group: id, Actor: id}, &chat.GroupDeleted{}},
		{"UpdateUserUpdateName", chat.UpdateUserUpdateName{ID: id, Name: "N"}, &chat.UpdateUserUpdateName{}},
		{"UpdateUserUpdateImage", chat.UpdateUserUpdateImage{ID: id}, &chat.UpdateUserUpdateImage{}},
		{"InitialState", chat.InitialState{NetworkOnline: true}, &chat.InitialState{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.val)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if err := json.Unmarshal(b, tc.into); err != nil {
				t.Fatalf("unmarshal of %s: %v", b, err)
			}
		})
	}
}

// prepareAttachments is the one piece of real logic in the binding, so its
// failure modes are worth pinning: a bad path must not leak open files.
func TestPrepareAttachmentsRejectsMissingFile(t *testing.T) {
	_, err := prepareAttachments(`[{"id":"550e8400-e29b-41d4-a716-446655440000","path":"/nonexistent/nope","name":"nope"}]`)
	if err == nil {
		t.Fatal("expected an error for a missing attachment path")
	}
	if !strings.Contains(err.Error(), "opening attachment") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPrepareAttachmentsEmpty(t *testing.T) {
	p, err := prepareAttachments("")
	if err != nil {
		t.Fatalf("empty attachments should be fine: %v", err)
	}
	if len(p.readers) != 0 || len(p.images) != 0 || len(p.files) != 0 {
		t.Fatal("expected nothing prepared")
	}
}

func TestPrepareAttachmentsRejectsBadUUID(t *testing.T) {
	_, err := prepareAttachments(`[{"id":"not-a-uuid","path":"/tmp","name":"x"}]`)
	if err == nil || !strings.Contains(err.Error(), "invalid attachment id") {
		t.Fatalf("expected an attributable uuid error, got %v", err)
	}
}
