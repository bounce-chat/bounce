package chat

import (
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

func (b *bounce) markRead(id uuid.UUID, frameType string) {
	log.WithFields(log.Fields{"id": id, "type": frameType}).Warn("read message")
	// TODO: mark read on the frame
	// TODO: make read on any earlier frames of the same type
	// TODO: send read receipts if enabled
}
