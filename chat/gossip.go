package chat

func (bounce *Bounce) gossip() {
	// look up the state from the database
	// decide which devices should be dialed to integrate into the network
	// populate the device pool
}

func (bounce *Bounce) broadcastDirectMessage(dm directMessage) {
	// marshall with messagepack
	// get the devices in scope (all user devices and all sync devices)
	// write the frame to all
}
