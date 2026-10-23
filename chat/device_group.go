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

// Maximum number of devices that can ever be associated with a profile for the lifetime of that profile
const maximumDevicesPerUser = 1024

// A new device can be added to a device group if:
//  1. it belongs to the user
//  2. it doesn't already exist
//  3. adding it wouldn't put the device group over the size limit
//  4. the new device has a valid mutual signature with an existing device
//  5. the existing device was not revoked before it signed this new device
func (b *Bounce) isValidAddition(u user, dev device) bool {
	// New device must have ID generated from content
	if !idMatchesContent(&dev) {
		log.WithFields(log.Fields{
			"user":   u.ID,
			"device": dev.ID,
		}).Warn("rejecting device addition because the device ID is not generated from its content")
		return false
	}

	// New device must belong to the user
	if dev.UserID != u.ID {
		log.WithFields(log.Fields{
			"user":   u.ID,
			"device": dev.ID,
			"owner":  dev.UserID,
		}).Warn("rejecting device addition because the device belongs to another user")
		return false
	}

	// New device should not already exist
	if existing, exists := b.getDeviceFromAddress(dev.Address); exists {
		if existing.UserID == u.ID {
			log.WithFields(log.Fields{
				"user":    u.ID,
				"device":  dev.ID,
				"address": dev.Address,
			}).Warn("rejecting device addition because the address is already one of the user's devices")
		} else {
			log.WithFields(log.Fields{
				"user":    u.ID,
				"device":  dev.ID,
				"address": dev.Address,
				"owner":   existing.UserID,
			}).Warn("rejecting device addition because the address is already a device of another user")
		}
		return false
	}

	// Load any devices that are part of this user from the database
	storedDevices := []device{}
	b.database.Where("user_id = ?", u.ID).Find(&storedDevices)
	knownDevices := map[string]device{}
	for _, known := range u.Devices {
		if known.Address != dev.Address {
			knownDevices[known.Address] = known
		}
	}
	for _, stored := range storedDevices {
		knownDevices[stored.Address] = stored
	}

	// New device must not exceed cap on number of devices
	if len(knownDevices) >= maximumDevicesPerUser {
		log.WithFields(log.Fields{
			"user":    u.ID,
			"device":  dev.ID,
			"devices": len(knownDevices),
		}).Warn("rejecting device addition because the user has the maximum number of devices")
		return false
	}

	// Do not allow additions to empty device groups
	if len(knownDevices) == 0 {
		log.WithFields(log.Fields{
			"user":   u.ID,
			"device": dev.ID,
		}).Warn("rejecting device addition for a user with no devices")
		return false
	}

	// New device must carry a signature from an existing device
	if dev.Signature == nil {
		log.WithFields(log.Fields{
			"user":   u.ID,
			"device": dev.ID,
		}).Warn("rejecting device addition because a device group can only have one unsigned device")
		return false
	}
	introducer, known := knownDevices[dev.Signature.PreexistingDevice]
	if !known {
		log.WithFields(log.Fields{
			"user":       u.ID,
			"device":     dev.ID,
			"introducer": dev.Signature.PreexistingDevice,
		}).Warn("rejecting device addition because it was introduced by a device that is not one of the user's devices")
		return false
	}

	// The introducing device cannot be revoked
	if introducer.RevokedAt != 0 && introducer.RevokedAt < dev.Timestamp {
		log.WithFields(log.Fields{
			"user":       u.ID,
			"device":     dev.ID,
			"introducer": introducer.Address,
		}).Warn("rejecting device addition because it was introduced by a device that had already been revoked")
		return false
	}

	// Verify the mutual signature
	introducerSignsNew := b.network.VerifySignature(introducer.Address, []byte(dev.Address), dev.Signature.SignatureOfNewDevice)
	newSignsIntroducer := b.network.VerifySignature(dev.Address, []byte(introducer.Address), dev.Signature.SignatureOfPreexistingDevice)
	if !introducerSignsNew || !newSignsIntroducer {
		log.WithFields(log.Fields{
			"user":       u.ID,
			"device":     dev.ID,
			"introducer": introducer.Address,
		}).Warn("rejecting device addition because an introduction signature is invalid")
		return false
	}

	return true
}

// Check if the devices belonging to this user constitute a valid device group
func (b *Bounce) hasValidDeviceGroup(u user) bool {
	dg := &deviceGroup{}
	originalDevice := ""
	signers := map[string]bool{}

	if len(u.Devices) == 0 {
		return false
	}

	seen := map[string]bool{}
	for _, dev := range u.Devices {
		seen[dev.Address] = true
	}
	preexistingDevices := []device{}
	b.database.Where("user_id = ?", u.ID).Find(&preexistingDevices)
	revokedTimes := map[string]int64{}
	for _, dev := range preexistingDevices {
		if !seen[dev.Address] {
			seen[dev.Address] = true
			u.Devices = append(u.Devices, dev)
		}
		revokedTimes[dev.Address] = dev.RevokedAt
	}
	for _, dev := range u.Devices {
		if _, ok := revokedTimes[dev.Address]; !ok {
			revokedTimes[dev.Address] = dev.RevokedAt
		}
	}

	if len(seen) > maximumDevicesPerUser {
		log.WithFields(log.Fields{
			"user":    u.ID,
			"devices": len(seen),
		}).Warn("a device group is invalid because it has more than the maximum number of devices")
		return false
	}

	for _, dev := range u.Devices {
		if dev.UserID != u.ID {
			return false
		}
		if !idMatchesContent(&dev) {
			log.WithFields(log.Fields{
				"user":   u.ID,
				"device": dev.ID,
			}).Warn("a device group is invalid because a device ID is not generated from its content")
			return false
		}

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
	if len(dg.signatures) == 0 && len(u.Devices) == 1 {
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
