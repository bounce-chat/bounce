package chat

import (
	"context"

	"github.com/hkparker/bounce/protocol"
)

func (bounce *BounceChat) ReceiveMessage(_ context.Context, chatMessage *protocol.ChatMessage) (*protocol.Errors, error) {
	// do the things that need to happen (verify, floodfill, write to db, etc)
	// bounce.ui.NewMessage(chatMessage)
	return &protocol.Errors{}, nil
}
