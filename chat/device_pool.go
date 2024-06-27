package chat

import (
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const poolTypeUser = 0
const poolTypeGroup = 1

const connectionsPerDevice = 2
const connectionsPerThread = 4
const startupDialsPerThread = 50
const dialCooldown = time.Duration(30 * time.Second)
const failedDialCooldown = time.Duration(30 * time.Minute)
const auditFrequency = time.Duration(60 * time.Second)
const keepAliveFrequency = time.Duration(15 * time.Second)

//
// The device pool is responsible for peering.  It stores all of the remote devices bounce is aware of in the devices field,
// and these are used when broadcasting to collect a set of devices that are in scope for a frame.  The device pool needs to
// ensure that connections are made to peer with specific groups and users, and to ensure there's no bifurcation of groups
// it needs to keep track of if the intention of a connection was to peer with a user or a group that user is in.  To
// accomplish this, two maps exist in the device pool to keep track of which remote devices were connected to to establish
// peering with users or groups.
//
type devicePool struct {
	auditing           sync.Mutex
	poolMutex          sync.Mutex
	deviceMutex        sync.Mutex
	onlineMutex        sync.Mutex
	devices            map[string]*remoteDevice
	groupPools         map[uuid.UUID][]*remoteDevice
	userPools          map[uuid.UUID][]*remoteDevice
	userOnlineStatus   map[uuid.UUID]bool
	deviceOnlineStatus map[uuid.UUID]bool
	lastDialMutex      sync.Mutex
	lastDial           map[string]time.Time
	lastFailedDial     map[string]time.Time
}

func (b *bounce) peer() {
	b.makeInitialPeeringConnections()
	go b.sendKeepAlives()
	ticker := time.NewTicker(auditFrequency)
	for _ = range ticker.C {
		b.auditPeers()
	}
}

func (b *bounce) makeInitialPeeringConnections() {
	b.devicePool.auditing.Lock()
	defer b.devicePool.auditing.Unlock()

	b.connectToSyncDevices()
	b.connectToGroups(startupDialsPerThread)
	b.connectToUsers(startupDialsPerThread)
	b.connectToCustomScopes(startupDialsPerThread)
}

func (b *bounce) auditPeers() {
	b.devicePool.auditing.Lock()
	defer b.devicePool.auditing.Unlock()

	// Skip this audit if the network isn't online
	if !b.networkIsOnline {
		return
	}

	// Always try to keep a socket open to every sync device
	b.connectToSyncDevices()

	// Connect to any groups we have pending messages for or who we talk to frequently
	b.connectToGroups(connectionsPerThread)

	// Connect to any users we have pending messages for or who we talk to frequently
	b.connectToUsers(connectionsPerThread)

	// Connect to any devices that we have frames for from a deleted group
	b.connectToCustomScopes(connectionsPerThread)

	// Close any extra connections we aren't using
	b.closeUnusedConnections()

	// Dial additional sockets for devices we're already connected to if needed
	b.dialMissingSockets()
}

func (b *bounce) sendKeepAlives() {
	ticker := time.NewTicker(keepAliveFrequency)
	for _ = range ticker.C {
		b.devicePool.deviceMutex.Lock()
		for _, rd := range b.devicePool.devices {
			if rd.connectedSockets > 0 {
				rd.messages <- keepAlive{}
			}
		}
		b.devicePool.deviceMutex.Unlock()
	}
}

func (b *bounce) connectToSyncDevices() {
	currentUser, exists := b.currentUser()
	if !exists {
		return
	}

	for _, dev := range currentUser.Devices {
		if dev.Address == b.network.Address() {
			continue
		}
		rd := b.getRemoteDevice(dev.Address)
		if rd.connectedSockets < connectionsPerDevice {
			go b.tryDialing(dev.Address)
		}
	}
}

func (b *bounce) connectToGroups(desiredConnections int) {
	// Find all recently active groups
	var activeGroups []group
	aMonthAgo := time.Now().Add(-4 * 7 * 24 * time.Hour).Unix()
	err := b.database.Preload("Users.Devices").Preload(clause.Associations).Where("last_activity > ?", aMonthAgo).Find(&activeGroups).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading groups to peer with")
	}

	for _, g := range activeGroups {
		// Prune the pool of closed connections
		b.devicePool.poolMutex.Lock()
		b.prunePool(poolTypeGroup, g.ID)
		b.devicePool.poolMutex.Unlock()

		// Collect all the devices associated with this group that are not on dial cooldown
		groupAddresses := []string{}
		for _, u := range g.Users {
			for _, dev := range u.Devices {
				if !b.shouldCooldownDial(dev.Address) && dev.Address != b.network.Address() {
					groupAddresses = append(groupAddresses, dev.Address)
				}
			}
		}

		// Choose a random selection of those devices in order to fill the pool
		b.devicePool.poolMutex.Lock()
		addressesToDial := chooseN(groupAddresses, desiredConnections-len(b.devicePool.groupPools[g.ID]))
		b.devicePool.poolMutex.Unlock()

		// Attempt to dial them
		for _, address := range addressesToDial {
			rd := b.getRemoteDevice(address)
			if rd.connectedSockets > 0 {
				// If we're already connected to this device, we can just associate the existing connection with this group
				b.insertRemoteDeviceIntoPool(address, poolTypeGroup, g.ID)
			} else {
				// If we have no connections to this device, try to dial it and associate the connection with the group
				go b.tryDialingAndAssociateWithGroup(address, g.ID)
			}
		}
	}
}

func (b *bounce) connectToUsers(desiredConnections int) {
	// Connect to any users we have interacted with recently
	var activeUsers []user
	aMonthAgo := time.Now().Add(-4 * 7 * 24 * time.Hour).Unix()
	err := b.database.Preload(clause.Associations).Where("profile = ? AND last_activity > ?", false, aMonthAgo).Find(&activeUsers).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading users to peer with")
	}

	for _, u := range activeUsers {
		// Prune the pool of closed connections
		b.devicePool.poolMutex.Lock()
		b.prunePool(poolTypeUser, u.ID)
		b.devicePool.poolMutex.Unlock()

		// Collect all the devices associated with this user that are not on dial cooldown
		unconnectedUserAddresses := []string{}
		for _, dev := range u.Devices {
			if !b.shouldCooldownDial(dev.Address) && dev.Address != b.network.Address() {
				rd := b.getRemoteDevice(dev.Address)
				if rd.connectedSockets == 0 {
					unconnectedUserAddresses = append(unconnectedUserAddresses, dev.Address)
				}
			}
		}

		// Choose a random selection of those devices in order to fill the pool
		b.devicePool.poolMutex.Lock()
		addressesToDial := chooseN(unconnectedUserAddresses, desiredConnections-len(b.devicePool.userPools[u.ID]))
		b.devicePool.poolMutex.Unlock()

		// Attempt to dial them
		for _, address := range addressesToDial {
			go b.tryDialing(address)
		}
	}

	// Try to connect to any users we have not interacted with recently, but that we have non-group messages for
	var inactiveUsers []user
	err = b.database.Preload(clause.Associations).Where("profile = ? AND last_activity < ?", false, aMonthAgo).Find(&inactiveUsers).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading inactive users")
	}
	for _, u := range activeUsers {
		for _, dev := range u.Devices {
			references := b.getReferenceOfferFor(dev.Address)
			rd := b.getRemoteDevice(dev.Address)
			// Dial this inactive user if we have non-global content that isn't just group messages
			if references.shouldDialUser() && !references.onlyGroupContent() && rd.connectedSockets == 0 {
				go b.tryDialing(dev.Address)
			}
		}
	}
}

func (b *bounce) connectToCustomScopes(desiredConnections int) {
	// treat each custom scope like a group, use the same logic otherwise
	// Connect to all gustom scopes
	var customScopes []customScope
	threeMonthsAgo := time.Now().Add(-3 * 4 * 7 * 24 * time.Hour).Unix()
	err := b.database.Where("created_at > ?", threeMonthsAgo).Find(&customScopes).Error
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error loading custom scopes to peer with")
	}

	for _, cs := range customScopes {
		// Prune the pool of closed connections
		b.devicePool.poolMutex.Lock()
		b.prunePool(poolTypeGroup, cs.ID)
		b.devicePool.poolMutex.Unlock()

		// Collect all the devices associated with this custom scope that are not on dial cooldown
		scopeAddresses := []string{}
		for _, address := range cs.addresses() {
			if !b.shouldCooldownDial(address) && address != b.network.Address() {
				scopeAddresses = append(scopeAddresses, address)
			}
		}

		// Choose a random selection of those devices in order to fill the pool
		b.devicePool.poolMutex.Lock()
		addressesToDial := chooseN(scopeAddresses, desiredConnections-len(b.devicePool.groupPools[cs.ID]))
		b.devicePool.poolMutex.Unlock()

		// Attempt to dial them
		for _, address := range addressesToDial {
			rd := b.getRemoteDevice(address)
			if rd.connectedSockets > 0 {
				// If we're already connected to this device, we can just associate the existing connection with this group
				b.insertRemoteDeviceIntoPool(address, poolTypeGroup, cs.ID)
			} else {
				// If we have no connections to this device, try to dial it and associate the connection with the group
				go b.tryDialingAndAssociateWithGroup(address, cs.ID)
			}
		}
	}
}

func (b *bounce) closeUnusedConnections() {
	b.devicePool.poolMutex.Lock()

	// Close any extra connections to any groups
	for groupID, _ := range b.devicePool.groupPools {
		// Prune the pool
		b.prunePool(poolTypeGroup, groupID)

		// If there are more connections than needed, close one at random
		if len(b.devicePool.groupPools[groupID]) > connectionsPerThread {
			log.WithFields(log.Fields{
				"group_id": groupID,
			}).Debug("closing unneeded connection to group")
			rand.Seed(time.Now().UnixNano())
			index := rand.Intn(len(b.devicePool.groupPools[groupID]))
			b.devicePool.groupPools[groupID][index].shutdown()
			b.devicePool.groupPools[groupID][index].closer.Wait()
			b.prunePool(poolTypeGroup, groupID)
		}

	}

	// Close any extra connections to any users
	myUser, profileExists := b.currentUser()
	for userID, _ := range b.devicePool.userPools {
		// Don't close connections to sync devices
		if profileExists && userID == myUser.ID {
			continue
		}

		// Prune the pool
		b.prunePool(poolTypeUser, userID)

		// If there are more connections than needed, close one at random
		if len(b.devicePool.userPools[userID]) > connectionsPerThread {
			log.WithFields(log.Fields{
				"user_id": userID,
			}).Debug("closing unneeded connection to user")
			rand.Seed(time.Now().UnixNano())
			index := rand.Intn(len(b.devicePool.userPools[userID]))
			b.devicePool.userPools[userID][index].shutdown()
			b.devicePool.userPools[userID][index].closer.Wait()
			b.prunePool(poolTypeUser, userID)
		}
	}
	b.devicePool.poolMutex.Unlock()

	// If a device has more sockets open than needed, close one at random
	b.devicePool.deviceMutex.Lock()
	for _, rd := range b.devicePool.devices {
		if rd.connectedSockets > connectionsPerDevice {
			log.WithFields(log.Fields{
				"connected_sockets": rd.connectedSockets,
				"desired_sockets":   connectionsPerDevice,
			}).Debug("closing a socket to a device")

			// Collect the keys
			keys := []uuid.UUID{}
			for k, _ := range rd.shutdownReceivers {
				keys = append(keys, k)
			}

			// Choose a random key
			rand.Seed(time.Now().UnixNano())
			index := rand.Intn(len(keys))
			key := keys[index]

			// Close that socket
			select {
			case rd.shutdownReceivers[key] <- true:
			default:
				log.WithFields(log.Fields{
					"connected_sockets": rd.connectedSockets,
					"desired_sockets":   connectionsPerDevice,
				}).Warn("failed to close socket on remote device")
			}

		}
	}
	b.devicePool.deviceMutex.Unlock()
}

func (b *bounce) dialMissingSockets() {
	b.devicePool.deviceMutex.Lock()
	defer b.devicePool.deviceMutex.Unlock()

	for address, rd := range b.devicePool.devices {
		if rd.connectedSockets > 0 && rd.connectedSockets < connectionsPerDevice {
			go b.tryDialing(address)
		}
	}
}

func (b *bounce) userConnectionDesired(id uuid.UUID) {
	if id == b.currentUserID() {
		// We always connect to sync devices, this is probably called because the UI
		// opened a thread to ourselves, we can ignore
		return
	}

	// Look up the user
	var u user
	err := b.database.Preload(clause.Associations).First(&u, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"user_id": id,
			}).Error("connection desired to unknown user")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up user")
		}
	}

	// Prune the pool of closed connections
	b.devicePool.poolMutex.Lock()
	b.prunePool(poolTypeUser, u.ID)
	b.devicePool.poolMutex.Unlock()

	// If we have no connections to this user, try to dial a large number of devices
	b.devicePool.poolMutex.Lock()
	connectedCount := len(b.devicePool.userPools[u.ID])
	b.devicePool.poolMutex.Unlock()
	if connectedCount == 0 {
		// Collect all the devices associated with this user that are not on dial cooldown
		userAddresses := []string{}
		for _, dev := range u.Devices {
			if !b.shouldCooldownDial(dev.Address) {
				userAddresses = append(userAddresses, dev.Address)
			}
		}

		// Choose a random selection of those devices in order to fill the pool
		addressesToDial := chooseN(userAddresses, startupDialsPerThread)

		// Attempt to dial them
		for _, address := range addressesToDial {
			go b.tryDialing(address)
		}
	}
}

func (b *bounce) groupConnectionDesired(id uuid.UUID) {
	// Look up the group
	var g group
	err := b.database.Preload("Users.Devices").Preload(clause.Associations).First(&g, "id = ?", id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			log.WithFields(log.Fields{
				"group_id": id,
			}).Error("connection desired to unknown group")
			return
		} else {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Fatal("database error looking up group")
		}
	}

	// Prune the pool of closed connections
	b.devicePool.poolMutex.Lock()
	b.prunePool(poolTypeGroup, g.ID)
	b.devicePool.poolMutex.Unlock()

	// If we have no connections to this group, try to dial a large number of devices
	b.devicePool.poolMutex.Lock()
	connectedCount := len(b.devicePool.groupPools[g.ID])
	b.devicePool.poolMutex.Unlock()
	if connectedCount == 0 {
		// Collect all the devices associated with this group that are not on dial cooldown
		groupAddresses := []string{}
		for _, u := range g.Users {
			for _, dev := range u.Devices {
				if !b.shouldCooldownDial(dev.Address) && dev.Address != b.network.Address() {
					groupAddresses = append(groupAddresses, dev.Address)
				}
			}
		}

		// Choose a random selection of those devices in order to fill the pool
		addressesToDial := chooseN(groupAddresses, startupDialsPerThread)

		// Attempt to dial them
		for _, address := range addressesToDial {
			go b.tryDialingAndAssociateWithGroup(address, id)
		}

	}
}

func (b *bounce) tryDialingAndAssociateWithGroup(address string, groupID uuid.UUID) {
	if b.tryDialing(address) {
		b.insertRemoteDeviceIntoPool(address, poolTypeGroup, groupID)
	}
}

func (b *bounce) tryDialing(address string) bool {
	if !b.networkIsOnline {
		log.WithFields(log.Fields{
			"address": address,
		}).Debug("ignoring request to dial while network is offline")
		return false
	}
	if b.shouldCooldownDial(address) {
		log.WithFields(log.Fields{
			"address": address,
		}).Debug("avoiding dial because of cooldown period")
		return false
	}
	if address == b.network.Address() {
		log.Warn("ignoring request to dial self")
	}

	b.devicePool.updateLastDial(address)
	log.WithFields(log.Fields{
		"peer": address,
	}).Debug("attempting to dial")
	conn, err := b.network.Dial(address)
	if err != nil {
		b.devicePool.updateLastFailedDial(address)
		log.WithFields(log.Fields{
			"peer":  address,
			"error": err.Error(),
		}).Debug("error dialing")
	} else {
		log.WithFields(log.Fields{
			"peer": address,
		}).Debug("dialed")
		b.insertConnectionIntoDevicePool(conn)
		return true
	}

	return false
}

func (b *bounce) insertRemoteDeviceIntoPool(address string, poolType int, id uuid.UUID) {
	b.devicePool.poolMutex.Lock()
	defer b.devicePool.poolMutex.Unlock()

	rd := b.getRemoteDevice(address)
	if poolType == poolTypeUser {
		currentPool, ok := b.devicePool.userPools[id]
		if !ok {
			b.devicePool.userPools[id] = []*remoteDevice{rd}
			return
		}
		alreadyIn := false
		for _, existingRD := range currentPool {
			if existingRD == rd {
				alreadyIn = true
			}
		}
		if !alreadyIn {
			b.devicePool.userPools[id] = append(b.devicePool.userPools[id], rd)
		}
	} else if poolType == poolTypeGroup {
		currentPool, ok := b.devicePool.groupPools[id]
		if !ok {
			b.devicePool.groupPools[id] = []*remoteDevice{rd}
			return
		}
		alreadyIn := false
		for _, existingRD := range currentPool {
			if existingRD == rd {
				alreadyIn = true
			}
		}
		if !alreadyIn {
			b.devicePool.groupPools[id] = append(b.devicePool.groupPools[id], rd)
		}
	} else {
		log.WithFields(log.Fields{
			"pool_type": poolType,
		}).Fatal("cannot associate connection with unknown pool type")
	}
}

func (b *bounce) prunePool(poolType int, id uuid.UUID) {
	if poolType == poolTypeUser {
		_, ok := b.devicePool.userPools[id]
		if !ok {
			b.devicePool.userPools[id] = []*remoteDevice{}
		}
		alivePool := []*remoteDevice{}
		for _, rd := range b.devicePool.userPools[id] {
			if rd.connectedSockets > 0 {
				alivePool = append(alivePool, rd)
			}
		}
		b.devicePool.userPools[id] = alivePool
	} else if poolType == poolTypeGroup {
		_, ok := b.devicePool.groupPools[id]
		if !ok {
			b.devicePool.groupPools[id] = []*remoteDevice{}
		}
		alivePool := []*remoteDevice{}
		for _, rd := range b.devicePool.groupPools[id] {
			if rd.connectedSockets > 0 {
				alivePool = append(alivePool, rd)
			}
		}
		b.devicePool.groupPools[id] = alivePool
	} else {
		log.WithFields(log.Fields{
			"pool_type": poolType,
		}).Fatal("cannot prune unknown pool type")
	}
}

func (dp *devicePool) getLastDial(address string) time.Time {
	dp.lastDialMutex.Lock()
	defer dp.lastDialMutex.Unlock()

	t, ok := dp.lastDial[address]
	if !ok {
		t = time.Time{}
		dp.lastDial[address] = t
	}
	return t
}

func (dp *devicePool) getLastFailedDial(address string) time.Time {
	dp.lastDialMutex.Lock()
	defer dp.lastDialMutex.Unlock()

	t, ok := dp.lastFailedDial[address]
	if !ok {
		t = time.Time{}
		dp.lastFailedDial[address] = t
	}
	return t
}

func (dp *devicePool) updateLastDial(address string) {
	dp.lastDialMutex.Lock()
	defer dp.lastDialMutex.Unlock()

	dp.lastDial[address] = time.Now()
}

func (dp *devicePool) updateLastFailedDial(address string) {
	dp.lastDialMutex.Lock()
	defer dp.lastDialMutex.Unlock()

	dp.lastFailedDial[address] = time.Now()
}

func (b *bounce) shouldCooldownDial(address string) bool {
	lastDial := b.devicePool.getLastDial(address)
	lastFailedDial := b.devicePool.getLastFailedDial(address)
	if time.Now().After(lastDial.Add(dialCooldown)) && time.Now().After(lastFailedDial.Add(failedDialCooldown)) {
		return false
	}
	return true
}

func (b *bounce) updateUserOnlineStatus(address string) {
	b.devicePool.onlineMutex.Lock()
	defer b.devicePool.onlineMutex.Unlock()

	// Ignore unknown devices
	dev, exists := b.getDeviceFromAddress(address)
	if !exists {
		return
	}
	// Ignore if we don't have a profile yet
	u, exists := b.currentUser()
	if !exists {
		return
	}

	// Get the remote device
	rd := b.getRemoteDevice(address)

	// Check if the user is currently online
	online := rd.connectedSockets > 0

	// Track sync devices on a per-device basis
	if dev.UserID == u.ID {
		// Get the current state for this device
		knownOnline, ok := b.devicePool.deviceOnlineStatus[dev.ID]
		if !ok {
			b.devicePool.deviceOnlineStatus[dev.ID] = false
			knownOnline = false
		}

		// Update the UI and cache if there's a state change
		if online && !knownOnline {
			b.devicePool.deviceOnlineStatus[dev.ID] = true
			b.userInterface.DeviceOnline(dev.ID)
		} else if !online && knownOnline {
			b.devicePool.deviceOnlineStatus[dev.ID] = false
			b.userInterface.DeviceOffline(dev.ID)
		}
	} else {
		// Get the current state for this user
		knownOnline, ok := b.devicePool.userOnlineStatus[dev.UserID]
		if !ok {
			b.devicePool.userOnlineStatus[dev.UserID] = false
			knownOnline = false
		}

		// Update the UI and cache if there's a state change
		if online && !knownOnline {
			b.devicePool.userOnlineStatus[dev.UserID] = true
			b.userInterface.UserIsOnline(dev.UserID)
		} else if !online && knownOnline {
			b.devicePool.userOnlineStatus[dev.UserID] = false
			b.userInterface.UserIsOffline(dev.UserID)
		}
	}
}

func chooseN(set []string, n int) []string {
	if n < 0 {
		return []string{}
	}
	if len(set) < n {
		return set
	}
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	order := r.Perm(len(set))
	picks := order[0:n]
	results := []string{}
	for _, pick := range picks {
		results = append(results, set[pick])
	}
	return results
}
