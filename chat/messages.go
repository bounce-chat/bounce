package chat

import (
	"github.com/google/uuid"
)

type message struct {
	ID          uuid.UUID `gorm:"type:uuid;primary_key;"`
	CreatedAt   int64
	ReceivedAt  int64
	Read        bool `msgpack:"-"`
	Source      uuid.UUID
	Destination uuid.UUID
	Text        string
	// TODO: other things that can be in a message, like a reference to an image, audio, video, or file attachment
	DeliveredTo string `msgpack:"-"` // Comma-separated list of addresses that have acked this message.  TODO: make an actual relation to the devices?
	payload     []byte
}
