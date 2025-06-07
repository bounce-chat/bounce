package chat

import log "github.com/sirupsen/logrus"

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

func dfs(n *node) {
	n.walked = true

	for _, connection := range n.connections {
		if !connection.walked {
			dfs(connection)
		}
	}
}

//
// Check if the addition of a new device to a user would result in a valid device group
//
func (b *Bounce) isValidAddition(u user, dev device) bool {
	u.Devices = append(u.Devices, dev)
	return b.hasValidDeviceGroup(u)
}

//
// Check if the devices belonging to this user constitute a valid device group
//
func (b *Bounce) hasValidDeviceGroup(u user) bool {
	dg := &deviceGroup{}
	originalDevice := ""
	signers := map[string]bool{}

	if len(u.Devices) == 0 {
		return false
	}

	revokedTimes := map[string]int64{}
	for _, dev := range u.Devices {
		revokedTimes[dev.Address] = dev.RevokedAt
	}

	for _, dev := range u.Devices {
		if dev.Signature != nil {
			dg.signatures = append(
				dg.signatures,
				mutualDeviceSignature{
					DeviceOne:   dev.Address,
					DeviceTwo:   dev.Signature.PreexistingDevice,
					OneSignsTwo: dev.Signature.SignatureOfPreexistingDevice,
					TwoSignsOne: dev.Signature.SignatureOfNewDevice,
				},
			)
			signers[dev.Signature.PreexistingDevice] = true

			// Make sure no devices were added by revoked devices
			signerRevokedTime, ok := revokedTimes[dev.Signature.PreexistingDevice]
			if !ok {
				log.WithFields(log.Fields{
					"signer": dev.Signature.PreexistingDevice,
				}).Warn("no revoked time for device that added another device to a device group")
				return false
			}
			if signerRevokedTime != 0 && signerRevokedTime < dev.Timestamp {
				log.WithFields(log.Fields{
					"user": u.ID,
				}).Warn("a device group is invalid because a device was added by another device that had already been revoked")
				return false
			}
		} else {
			if originalDevice != "" {
				// Can't have more than one device that was never signed
				log.WithFields(log.Fields{
					"user": u.ID,
				}).Warn("a device group is invalid because it has more than one unsigned devices")
				return false
			} else {
				originalDevice = dev.Address
			}
		}
	}

	// TODO: ensure there's at least one device that doesn't have an introduction signature?

	// An empty device group is valid
	if len(dg.signatures) == 0 {
		return true
	}

	// The original device that has no introduction signature must have been used to
	// sign at least one of the other devices
	if _, present := signers[originalDevice]; !present {
		log.WithFields(log.Fields{
			"original_device": originalDevice,
			"device_count":    len(u.Devices),
			"user":            u.ID,
		}).Warn("a device group is invalid because the original device did not sign any of the other devices")
		return false
	}

	sample := ""
	nodes := map[string]*node{}
	for _, pair := range dg.signatures {
		// Verify that the signatures are valid
		validSignature := b.network.VerifySignature(pair.DeviceOne, []byte(pair.DeviceTwo), pair.OneSignsTwo)
		if !validSignature {
			log.WithFields(log.Fields{
				"user":           u.ID,
				"signing_device": pair.DeviceOne,
				"target_device":  pair.DeviceTwo,
			}).Warn("a device group is invalid because an invalid signature")
			return false
		}
		validSignature = b.network.VerifySignature(pair.DeviceTwo, []byte(pair.DeviceOne), pair.TwoSignsOne)
		if !validSignature {
			log.WithFields(log.Fields{
				"user":           u.ID,
				"signing_device": pair.DeviceTwo,
				"target_device":  pair.DeviceOne,
			}).Warn("a device group is invalid because an invalid signature")
			return false
		}

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
			log.WithFields(log.Fields{
				"user": u.ID,
			}).Warn("a device group is invalid because the devices do not create a connected graph")
			return false
		}
	}
	return true
}
