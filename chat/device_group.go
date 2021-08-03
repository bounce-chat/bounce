package chat

func (u *user) validDeviceGroup() bool {
	// for each device the user owns
	//	make a mutual device signature from their introduction signature
	// create a device group from these mutual signatures
	// validate the group
	return true
}

func (u *user) validDeviceGroupAfterAddition(addition mutualDeviceSignature) bool {
	return true
}

type mutualDeviceSignature struct {
	DeviceOne   string
	DeviceTwo   string
	OneSignsTwo []byte
	TwoSignsOne []byte
}

type deviceGroup struct {
	signatures []mutualDeviceSignature
}

type node struct {
	walked      bool
	connections []*node
}

func (dg *deviceGroup) valid() bool {
	// Ensure the length is at least one

	sample := ""
	nodes := map[string]*node{}
	for _, pair := range dg.signatures {
		// validate both signatures are legit
		// Create a node for this device if it doesn't exist
		if _, exists := nodes[pair.DeviceOne]; !exists {
			nodes[pair.DeviceOne] = &node{}
		}
		if _, exists := nodes[pair.DeviceTwo]; !exists {
			nodes[pair.DeviceTwo] = &node{}
		}
		// Bidirectionally link them
		nodes[pair.DeviceOne].connections = append(
			nodes[pair.DeviceOne].connections,
			nodes[pair.DeviceTwo],
		)
		nodes[pair.DeviceTwo].connections = append(
			nodes[pair.DeviceTwo].connections,
			nodes[pair.DeviceOne],
		)
		// Get a starting point for the DFS
		sample = pair.DeviceOne
	}

	dfs(nodes[sample])

	for _, n := range nodes {
		if !n.walked {
			return false
		}
	}
	return true
}

func dfs(n *node) {
	n.walked = true

	for _, connection := range n.connections {
		if !connection.walked {
			dfs(connection)
		}
	}
}
