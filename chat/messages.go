package chat

import (
	"sync"

	"github.com/google/uuid"
)

type message struct {
	ID               uuid.UUID `gorm:"type:uuid;primary_key;"`
	SavedAt          int64     `msgpack:"-"`
	WrittenAt        int64
	RetentionSeconds int64 `msgpack:"-"` // Number of seconds to retain this message, captures the retention setting at the time the message was written
	DeleteAt         int64 `msgpack:"-"` // Absolute time at which the messages expires.  Time it was first acked/received + RetentionSeconds
	Read             bool  `msgpack:"-"`
	Undeliverable    bool  `msgpack:"-"` // The message was never delivered to another device and is beyond when we give up including it in reference offers
	Source           uuid.UUID
	Destination      uuid.UUID
	Text             string // TODO: other things that can be in a message, like a reference to an image, audio, video, or file attachment
	payload          []byte
	payloadMutex     sync.Mutex
}
