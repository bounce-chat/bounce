package chat

import (
	"net"
)

//func (bounce *Bounce) getHandlers() map[int]func([]byte) {
//	return map[int]func([]byte){
//		0: chat.handleChatMessage,
//	}
//}

func (bounce *Bounce) handleIncomingConnection(conn net.Conn) {
	// Get the peer address
	// reject it if it isn't a known device?  Maybe don't want to if introductions / group membership is out of order
	// if it is known, add this connection to the larger device structure, which will mark the owner's user as online
	//for {
	//	// Read the next struct
	//	// if there's errors, abondon the connection, remove this connection from the device structure
	//	// pass the bytes read to the function for the correct type
	//}
}

func (bounce *Bounce) handleChatMessage(payload []byte) {
	// protobuf unmarshal the bytes
	// put it in the database
	// send it to the UI
	// gossip it as needed
}
