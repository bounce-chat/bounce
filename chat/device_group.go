package chat

func (u *user) validDeviceGroup() bool {
	if len(u.Devices) == 0 {
		return false
	}

	originalDeviceFound := false
	deviceGroup := &deviceGroup{}
	for _, dev := range u.Devices {
		if dev.Signature != nil {
			deviceGroup.signatures = append(
				deviceGroup.signatures,
				mutualDeviceSignature{
					DeviceOne:   dev.Address,
					DeviceTwo:   dev.Signature.SigningDevice,
					OneSignsTwo: dev.Signature.SignatureOfSigningDevice,
					TwoSignsOne: dev.Signature.SigningDeviceSignature,
				},
			)
		} else {
			if originalDeviceFound {
				// Can't have more than one device that was never signed
				return false
			} else {
				originalDeviceFound = true
			}
		}
	}
	return deviceGroup.valid()
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
	if len(dg.signatures) == 0 {
		return true
	}

	sample := ""
	nodes := map[string]*node{}
	for _, pair := range dg.signatures {
		// TODO: validate both signatures are legit
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
