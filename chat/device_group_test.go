package chat

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
	"github.com/zeebo/blake3"
)

// A device group is validated in full whenever a whole user record arrives, but a single new device is checked only
// against the device that introduced it.  Every device we hold was accepted one of those two ways, so the stored
// group is already valid and the new device can only break the properties that involve it.  That keeps the cost of
// learning a device constant no matter how many a user has, which is what lets users have many of them.

// countingNetwork counts signature verifications so a test can say exactly how much work a check did
type countingNetwork struct {
	Network
	verifications atomic.Int64
}

func (n *countingNetwork) VerifySignature(address string, data []byte, signature []byte) bool {
	n.verifications.Add(1)
	return n.Network.VerifySignature(address, data, signature)
}

// validatorFor returns a validator over b's database whose signature verifications are counted.  The validators only
// touch the database and the network, so a bare engine with those two fields is enough and nothing running inside b
// sees the counting network.
func validatorFor(b *Bounce) (*Bounce, *countingNetwork) {
	counting := &countingNetwork{Network: b.network}
	return &Bounce{database: b.database, network: counting}, counting
}

// introducedBy builds a device for owner introduced by the device at introducer's address, with both signatures made
// by the keys given.  Passing a different key as introducerKey than the one behind the introducer address forges it.
func introducedBy(owner ID, introducer string, introducerKey *bnc01Onion, newDevice *bnc01Onion, timestamp int64) device {
	d := device{
		Nonce:     newNonce(),
		UserID:    owner,
		Address:   newDevice.addr(),
		Timestamp: timestamp,
		Signature: &introductionSignature{
			PreexistingDevice:            introducer,
			SignatureOfNewDevice:         introducerKey.sign([]byte(newDevice.addr())),
			SignatureOfPreexistingDevice: newDevice.sign([]byte(introducer)),
		},
	}
	d.ID = d.generateID()
	return d
}

// introducedByBounce is introducedBy for an introducer that is a running engine, which signs with its real key
func introducedByBounce(owner ID, introducer *Bounce, newDevice *bnc01Onion, timestamp int64) device {
	d := device{
		Nonce:     newNonce(),
		UserID:    owner,
		Address:   newDevice.addr(),
		Timestamp: timestamp,
		Signature: &introductionSignature{
			PreexistingDevice:            introducer.network.Address(),
			SignatureOfNewDevice:         introducer.network.Sign([]byte(newDevice.addr())),
			SignatureOfPreexistingDevice: newDevice.sign([]byte(introducer.network.Address())),
		},
	}
	d.ID = d.generateID()
	return d
}

// growDeviceGroup stores count more devices for owner in b's database, each legitimately introduced by introducer,
// as if they had been learned one at a time
func growDeviceGroup(t *testing.T, b *Bounce, owner ID, introducer *Bounce, count int) {
	t.Helper()
	added := make([]device, 0, count)
	for i := 0; i < count; i++ {
		added = append(added, introducedByBounce(owner, introducer, bnc01NewOnion(), time.Now().Unix()))
	}
	assert.NoError(t, b.database.CreateInBatches(&added, 100).Error)
}

func deviceCount(t *testing.T, b *Bounce, owner ID) int {
	t.Helper()
	var count int64
	assert.NoError(t, b.database.Model(&device{}).Where("user_id = ?", owner).Count(&count).Error)
	return int(count)
}

func TestLearningADeviceCostsTheSameHoweverManyTheUserHas(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	aliceID := alice.currentUserID()
	growDeviceGroup(t, me, aliceID, alice, 200)

	var aliceRecord user
	assert.NoError(t, me.database.Preload("Devices.Signature").First(&aliceRecord, "id = ?", aliceID).Error)
	assert.Equal(t, 201, len(aliceRecord.Devices))

	// One more device introduced by Alice's original: only its own two signatures are checked
	validator, counting := validatorFor(me)
	newest := introducedByBounce(aliceID, alice, bnc01NewOnion(), time.Now().Unix())
	assert.True(t, validator.isValidAddition(aliceRecord, newest))
	assert.Equal(t, int64(2), counting.verifications.Load())

	// Whereas taking the whole record at face value means checking every introduction in it
	counting.verifications.Store(0)
	assert.True(t, validator.hasValidDeviceGroup(aliceRecord))
	assert.Equal(t, int64(2*200), counting.verifications.Load())

	// And the device handler accepts it the same way
	_, accepted := me.handleDevice(alice.network.Address(), newest.getPayload(), false)
	assert.True(t, accepted)
	assert.Equal(t, 202, deviceCount(t, me, aliceID))
}

func TestANewDeviceIsOnlyAcceptedFromOneOfTheUsersDevices(t *testing.T) {
	me, alice, bob, _ := createUsersAndGroups(t)
	aliceID := alice.currentUserID()
	validator, _ := validatorFor(me)
	var aliceRecord user
	assert.NoError(t, me.database.Preload("Devices.Signature").First(&aliceRecord, "id = ?", aliceID).Error)
	now := time.Now().Unix()

	// The real thing, for contrast
	assert.True(t, validator.isValidAddition(aliceRecord, introducedByBounce(aliceID, alice, bnc01NewOnion(), now)))

	// Introduced by a device that is not Alice's, even one that signs correctly for itself
	stranger := bnc01NewOnion()
	assert.False(t, validator.isValidAddition(aliceRecord, introducedBy(aliceID, stranger.addr(), stranger, bnc01NewOnion(), now)),
		"the introducer must be one of the user's devices")
	assert.False(t, validator.isValidAddition(aliceRecord, introducedByBounce(aliceID, bob, bnc01NewOnion(), now)),
		"even a real device, when it belongs to someone else")

	// Introduced by Alice's device, but the introducer's signature is not hers
	assert.False(t, validator.isValidAddition(aliceRecord, introducedBy(aliceID, alice.network.Address(), stranger, bnc01NewOnion(), now)),
		"the introducer must have signed the new address")

	// The new device did not sign the introducer back
	forged := introducedByBounce(aliceID, alice, bnc01NewOnion(), now)
	forged.Signature.SignatureOfPreexistingDevice = stranger.sign([]byte(alice.network.Address()))
	assert.False(t, validator.isValidAddition(aliceRecord, forged), "the new device must have signed the introducer")

	// Claimed for a different user than the record it is checked against
	assert.False(t, validator.isValidAddition(aliceRecord, introducedByBounce(bob.currentUserID(), alice, bnc01NewOnion(), now)))

	// A second device with no introduction at all
	unsigned := device{Nonce: newNonce(), UserID: aliceID, Address: bnc01NewOnion().addr(), Timestamp: now}
	unsigned.ID = unsigned.generateID()
	assert.False(t, validator.isValidAddition(aliceRecord, unsigned),
		"only the original device is unsigned")

	// An address Alice already has
	again := introducedByBounce(aliceID, alice, bnc01NewOnion(), now)
	again.Address = alice.network.Address()
	again.Signature.SignatureOfNewDevice = alice.network.Sign([]byte(again.Address))
	again.Signature.SignatureOfPreexistingDevice = alice.network.Sign([]byte(alice.network.Address()))
	assert.False(t, validator.isValidAddition(aliceRecord, again))

	// An address that is Bob's device, even with Alice's own introduction of it in order
	taken := introducedByBounce(aliceID, alice, bnc01NewOnion(), now)
	taken.Address = bob.network.Address()
	taken.Signature.SignatureOfNewDevice = alice.network.Sign([]byte(taken.Address))
	taken.Signature.SignatureOfPreexistingDevice = bob.network.Sign([]byte(alice.network.Address()))
	assert.False(t, validator.isValidAddition(aliceRecord, taken), "a device cannot change hands")

	// The handler path rejects the same way and stores nothing
	_, accepted := me.handleDevice(alice.network.Address(), forged.getPayload(), false)
	assert.False(t, accepted)
	assert.Equal(t, 1, deviceCount(t, me, aliceID))
}

func TestIsValidAdditionUsesRevokeKeepList(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	aliceID := alice.currentUserID()
	validator, _ := validatorFor(me)

	second := bnc01NewOnion()
	secondDevice := introducedByBounce(aliceID, alice, second, 1000)
	assert.NoError(t, me.database.Create(&secondDevice).Error)
	listed := introducedBy(aliceID, second.addr(), second, bnc01NewOnion(), 2500)
	omitted := introducedBy(aliceID, second.addr(), second, bnc01NewOnion(), 1500)

	data, err := msgpack.Marshal([]ID{listed.ID})
	assert.NoError(t, err)
	revoke := updateDevice{
		Nonce:     newNonce(),
		Target:    secondDevice.ID,
		Type:      updateDeviceTypeRevoke,
		Data:      data,
		Timestamp: 2000,
	}
	revoke.ID = revoke.generateID()
	revokePayload, err := msgpack.Marshal(&revoke)
	assert.NoError(t, err)
	revokeFrame, err := msgpack.Marshal(alice.createSignedContainer(typeUpdateDevice, revokePayload))
	assert.NoError(t, err)
	_, accepted := me.handleUpdateDevice(alice.network.Address(), revokeFrame, false)
	assert.True(t, accepted)

	var aliceRecord user
	assert.NoError(t, me.database.Preload("Devices.Signature").First(&aliceRecord, "id = ?", aliceID).Error)
	assert.True(t, validator.isValidAddition(aliceRecord, listed), "a listed device is valid regardless of its timestamp")
	assert.False(t, validator.isValidAddition(aliceRecord, omitted), "an omitted device is invalid even when backdated")

	// A record that claims the second is not revoked does not get to override what we know
	for i := range aliceRecord.Devices {
		aliceRecord.Devices[i].RevokedAt = 0
	}
	assert.False(t, validator.isValidAddition(aliceRecord, omitted))
}

func TestRevokeDeviceListsDevicesIntroducedByTheTarget(t *testing.T) {
	alice := newBounceUser("Alice")
	t.Cleanup(alice.Shutdown)
	aliceID := alice.currentUserID()
	aliceAddr := alice.network.Address()
	now := time.Now().Unix()

	secondKey := bnc01NewOnion()
	second := introducedByBounce(aliceID, alice, secondKey, now-300)
	_, accepted := alice.handleDevice(aliceAddr, second.getPayload(), false)
	assert.True(t, accepted)

	introducedKey := bnc01NewOnion()
	introduced := introducedBy(aliceID, secondKey.addr(), secondKey, introducedKey, now-200)
	_, accepted = alice.handleDevice(secondKey.addr(), introduced.getPayload(), false)
	assert.True(t, accepted)

	assert.NoError(t, alice.RevokeDevice(second.ID))
	var revoke updateDevice
	assert.NoError(t, alice.database.Where("target = ? AND type = ?", second.ID, updateDeviceTypeRevoke).First(&revoke).Error)
	devicesToKeep := []ID{}
	assert.NoError(t, msgpack.Unmarshal(revoke.Data, &devicesToKeep))
	assert.Equal(t, []ID{introduced.ID}, devicesToKeep)

	_, kept := alice.getDeviceFromAddress(introduced.Address)
	assert.True(t, kept)
}

func TestRevokeKeepListControlsIntroducedDevices(t *testing.T) {
	me, alice, bob, _ := createUsersAndGroups(t)
	aliceID := alice.currentUserID()
	aliceAddr := alice.network.Address()
	now := time.Now().Unix()
	revokeTime := now - 10

	secondKey := bnc01NewOnion()
	second := introducedByBounce(aliceID, alice, secondKey, now-1000)
	for _, b := range []*Bounce{me, alice, bob} {
		_, accepted := b.handleDevice(aliceAddr, second.getPayload(), false)
		assert.True(t, accepted)
	}

	keptKey := bnc01NewOnion()
	keptDevice := introducedBy(aliceID, secondKey.addr(), secondKey, keptKey, revokeTime-100)
	for _, b := range []*Bounce{me, alice} {
		_, accepted := b.handleDevice(secondKey.addr(), keptDevice.getPayload(), false)
		assert.True(t, accepted)
	}

	backdatedKey := bnc01NewOnion()
	backdated := introducedBy(aliceID, secondKey.addr(), secondKey, backdatedKey, revokeTime-50)
	later := introducedBy(aliceID, secondKey.addr(), secondKey, bnc01NewOnion(), revokeTime+50)
	grandchild := introducedBy(aliceID, backdatedKey.addr(), backdatedKey, bnc01NewOnion(), revokeTime-40)
	for _, d := range []device{backdated, later, grandchild} {
		_, accepted := me.handleDevice(d.Signature.PreexistingDevice, d.getPayload(), false)
		assert.True(t, accepted)
	}

	data, err := msgpack.Marshal([]ID{keptDevice.ID})
	assert.NoError(t, err)
	revoke := updateDevice{
		Nonce:     newNonce(),
		Target:    second.ID,
		Type:      updateDeviceTypeRevoke,
		Data:      data,
		Timestamp: revokeTime,
	}
	revoke.ID = revoke.generateID()
	revokePayload, err := msgpack.Marshal(&revoke)
	assert.NoError(t, err)
	revokeFrame, err := msgpack.Marshal(alice.createSignedContainer(typeUpdateDevice, revokePayload))
	assert.NoError(t, err)

	for _, b := range []*Bounce{alice, me, bob} {
		_, accepted := b.handleUpdateDevice(aliceAddr, revokeFrame, false)
		assert.True(t, accepted)
	}

	_, exists := me.getDeviceFromAddress(keptDevice.Address)
	assert.True(t, exists, "the revoke explicitly retains this introduced device")
	for _, d := range []device{backdated, later, grandchild} {
		_, exists = me.getDeviceFromAddress(d.Address)
		assert.False(t, exists, "an introduced device omitted from the keep list cannot survive by backdating")
	}

	// Bob learned the revoke before any of the devices it introduced. The keep list controls those arrivals too.
	_, accepted := bob.handleDevice(aliceAddr, keptDevice.getPayload(), false)
	assert.True(t, accepted, "a listed device can still arrive after the revoke")
	for _, d := range []device{backdated, later, grandchild} {
		_, accepted = bob.handleDevice(aliceAddr, d.getPayload(), false)
		assert.False(t, accepted, "an omitted device cannot arrive after the revoke")
	}
}

func TestCarriedRecordsRespectRevokeKeepList(t *testing.T) {
	me, _, bob, groupID := createUsersAndGroups(t)
	bobID := bob.currentUserID()
	bobAddr := bob.network.Address()
	now := time.Now().Unix()
	revokeTime := now - 10

	secondKey := bnc01NewOnion()
	second := introducedByBounce(bobID, bob, secondKey, now-1000)
	_, accepted := me.handleDevice(bobAddr, second.getPayload(), false)
	assert.True(t, accepted)

	listedKey := bnc01NewOnion()
	listed := introducedBy(bobID, secondKey.addr(), secondKey, listedKey, revokeTime-100)
	omittedKey := bnc01NewOnion()
	omitted := introducedBy(bobID, secondKey.addr(), secondKey, omittedKey, revokeTime-90)
	descendant := introducedBy(bobID, omittedKey.addr(), omittedKey, bnc01NewOnion(), revokeTime-80)

	data, err := msgpack.Marshal([]ID{listed.ID})
	assert.NoError(t, err)
	revoke := updateDevice{
		Nonce:     newNonce(),
		Target:    second.ID,
		Type:      updateDeviceTypeRevoke,
		Data:      data,
		Timestamp: revokeTime,
	}
	revoke.ID = revoke.generateID()
	revokePayload, err := msgpack.Marshal(&revoke)
	assert.NoError(t, err)
	revokeFrame, err := msgpack.Marshal(bob.createSignedContainer(typeUpdateDevice, revokePayload))
	assert.NoError(t, err)
	_, accepted = me.handleUpdateDevice(bobAddr, revokeFrame, false)
	assert.True(t, accepted)

	var record user
	assert.NoError(t, me.database.Preload("Devices.Signature").First(&record, "id = ?", bobID).Error)
	record.Devices = append(record.Devices, descendant, omitted, listed)
	me.createNewUserIfNeeded(record, groupID)

	_, exists := me.getDeviceFromAddress(listed.Address)
	assert.True(t, exists, "a carried record can teach a device retained by the revoke")
	assertOmitted := func() {
		for _, d := range []device{omitted, descendant} {
			_, exists = me.getDeviceFromAddress(d.Address)
			assert.False(t, exists, "a carried record cannot plant an omitted device or its descendants")
		}
	}
	assertOmitted()

	projectedGroup := group{
		ID:        NewID(),
		Name:      "carried devices",
		CreatedBy: bobID,
		CreatedAt: now,
		Users:     []user{record},
		Admins:    bobID.String(),
	}
	assert.NoError(t, me.setGroupStateInDatabase(projectedGroup, []user{record}, projectedGroup.state(), nil))
	assertOmitted()
}

func TestAddUserCannotAdoptDeviceIntroducedByRevokedSyncDevice(t *testing.T) {
	me := newBounceUser("Me")
	t.Cleanup(me.Shutdown)
	meID := me.currentUserID()
	meAddr := me.network.Address()
	now := time.Now().Unix()
	revokeTime := now - 10

	revokedKey := bnc01NewOnion()
	revoked := introducedByBounce(meID, me, revokedKey, now-1000)
	_, accepted := me.handleDevice(meAddr, revoked.getPayload(), false)
	assert.True(t, accepted)

	data, err := msgpack.Marshal([]ID{})
	assert.NoError(t, err)
	revoke := updateDevice{
		Nonce:     newNonce(),
		Target:    revoked.ID,
		Type:      updateDeviceTypeRevoke,
		Data:      data,
		Timestamp: revokeTime,
	}
	revoke.ID = revoke.generateID()
	revokePayload, err := msgpack.Marshal(&revoke)
	assert.NoError(t, err)
	revokeFrame, err := msgpack.Marshal(me.createSignedContainer(typeUpdateDevice, revokePayload))
	assert.NoError(t, err)
	_, accepted = me.handleUpdateDevice(meAddr, revokeFrame, false)
	assert.True(t, accepted)

	backdatedKey := bnc01NewOnion()
	backdated := introducedBy(meID, revokedKey.addr(), revokedKey, backdatedKey, revokeTime-100)
	myRecord, ok := me.currentUser()
	assert.True(t, ok)
	myRecord.Devices = append(myRecord.Devices, backdated)

	counterpartyKey := bnc01NewOnion()
	counterpartyID := NewID()
	counterparty := user{
		ID:      counterpartyID,
		Name:    "Counterparty",
		Devices: []device{bnc01Original(counterpartyID, counterpartyKey.addr(), now)},
	}
	payload := bnc01BuildAddUser(t, counterparty, myRecord, counterpartyKey, backdatedKey)
	_, accepted = me.handleAddUser(bnc01NewOnion().addr(), payload, false)

	_, exists := me.getDeviceFromAddress(backdated.Address)
	assert.False(t, exists, "addUser cannot adopt a backdated device introduced by a revoked sync device")
	assert.False(t, accepted, "a device omitted by its introducer's revoke cannot sign for our side of an addUser")
	assert.False(t, me.signedByUser(&signedContainer{Signer: backdated.Address}, meID))
}

func TestAddUserAcceptsVerifiableUnknownSyncDevice(t *testing.T) {
	me := newBounceUser("Me")
	t.Cleanup(me.Shutdown)
	meID := me.currentUserID()
	now := time.Now().Unix()

	newKey := bnc01NewOnion()
	newDevice := introducedByBounce(meID, me, newKey, now)
	myRecord, ok := me.currentUser()
	assert.True(t, ok)
	myRecord.Devices = append(myRecord.Devices, newDevice)

	counterpartyKey := bnc01NewOnion()
	counterpartyID := NewID()
	counterparty := user{
		ID:      counterpartyID,
		Name:    "Counterparty",
		Devices: []device{bnc01Original(counterpartyID, counterpartyKey.addr(), now)},
	}
	payload := bnc01BuildAddUser(t, counterparty, myRecord, counterpartyKey, newKey)
	_, accepted := me.handleAddUser(counterpartyKey.addr(), payload, false)
	assert.True(t, accepted, "a valid mutual introduction allows a new sync device to authorize addUser")

	stored, exists := me.getDeviceFromAddress(newDevice.Address)
	assert.True(t, exists, "a verifiable sync device is retained")
	assert.Equal(t, meID, stored.UserID)
	assert.True(t, me.signedByUser(&signedContainer{Signer: newDevice.Address}, meID))
	var storedCounterparty user
	assert.NoError(t, me.database.Where("id = ?", counterpartyID).First(&storedCounterparty).Error)
	_, exists = me.getDeviceFromAddress(counterpartyKey.addr())
	assert.True(t, exists, "an accepted new user's devices are saved after the user")
}

func TestRejectedAddUserDoesNotSaveDevicesForNewCounterparty(t *testing.T) {
	me := newBounceUser("Me")
	t.Cleanup(me.Shutdown)
	now := time.Now().Unix()
	revokeTime := now - 10

	counterpartyID := NewID()
	originalKey := bnc01NewOnion()
	original := bnc01Original(counterpartyID, originalKey.addr(), now-1000)
	revokedKey := bnc01NewOnion()
	revoked := introducedBy(counterpartyID, originalKey.addr(), originalKey, revokedKey, now-900)
	assert.NoError(t, me.database.Create(&original).Error)
	assert.NoError(t, me.database.Create(&revoked).Error)

	data, err := msgpack.Marshal([]ID{})
	assert.NoError(t, err)
	revoke := updateDevice{
		Nonce:     newNonce(),
		Target:    revoked.ID,
		Type:      updateDeviceTypeRevoke,
		Data:      data,
		Timestamp: revokeTime,
	}
	revoke.ID = revoke.generateID()
	revokePayload, err := msgpack.Marshal(&revoke)
	assert.NoError(t, err)
	revokeFrame, err := msgpack.Marshal(signedContainer{
		Signer:    originalKey.addr(),
		Payload:   revokePayload,
		Signature: originalKey.sign(signedContainerHash(typeUpdateDevice, revokePayload)),
	})
	assert.NoError(t, err)
	_, accepted := me.handleUpdateDevice(originalKey.addr(), revokeFrame, false)
	assert.True(t, accepted)

	omittedKey := bnc01NewOnion()
	omitted := introducedBy(counterpartyID, revokedKey.addr(), revokedKey, omittedKey, revokeTime-100)
	validKey := bnc01NewOnion()
	validDevice := introducedBy(counterpartyID, originalKey.addr(), originalKey, validKey, now-800)
	counterparty := user{
		ID:      counterpartyID,
		Name:    "New counterparty",
		Devices: []device{original, revoked, validDevice, omitted},
	}
	myRecord, ok := me.currentUser()
	assert.True(t, ok)
	offerBytes, err := msgpack.Marshal(counterparty)
	assert.NoError(t, err)
	requesterBytes, err := msgpack.Marshal(myRecord)
	assert.NoError(t, err)
	offerHash := blake3.Sum256(offerBytes)
	requesterHash := blake3.Sum256(requesterBytes)
	au := addUser{
		Nonce:              newNonce(),
		Xor:                xor(counterpartyID, me.currentUserID()),
		Timestamp:          now,
		OfferUser:          offerBytes,
		RequesterUser:      requesterBytes,
		OfferDevice:        omitted.Address,
		RequesterDevice:    me.network.Address(),
		OfferSignature:     omittedKey.sign(requesterHash[:]),
		RequesterSignature: me.network.Sign(offerHash[:]),
	}
	au.ID = au.generateID()
	payload, err := msgpack.Marshal(&au)
	assert.NoError(t, err)
	_, accepted = me.handleAddUser(bnc01NewOnion().addr(), payload, false)
	assert.False(t, accepted)

	for _, d := range []device{validDevice, omitted} {
		_, exists := me.getDeviceFromAddress(d.Address)
		assert.False(t, exists, "a rejected new counterparty's devices must not be left behind")
	}
	for _, d := range []device{original, revoked} {
		_, exists := me.getDeviceFromAddress(d.Address)
		assert.True(t, exists, "devices that predated the addUser are untouched")
	}
	var storedCounterparty user
	assert.Error(t, me.database.Where("id = ?", counterpartyID).First(&storedCounterparty).Error)
}

func TestAddUserCarriedRecordsRespectRevokeKeepList(t *testing.T) {
	me, _, bob, _ := createUsersAndGroups(t)
	bobID := bob.currentUserID()
	bobAddr := bob.network.Address()
	now := time.Now().Unix()
	revokeTime := now - 10

	secondKey := bnc01NewOnion()
	second := introducedByBounce(bobID, bob, secondKey, now-1000)
	_, accepted := me.handleDevice(bobAddr, second.getPayload(), false)
	assert.True(t, accepted)

	listedKey := bnc01NewOnion()
	listed := introducedBy(bobID, secondKey.addr(), secondKey, listedKey, revokeTime-100)
	omittedKey := bnc01NewOnion()
	omitted := introducedBy(bobID, secondKey.addr(), secondKey, omittedKey, revokeTime-90)
	descendant := introducedBy(bobID, omittedKey.addr(), omittedKey, bnc01NewOnion(), revokeTime-80)

	data, err := msgpack.Marshal([]ID{listed.ID})
	assert.NoError(t, err)
	revoke := updateDevice{
		Nonce:     newNonce(),
		Target:    second.ID,
		Type:      updateDeviceTypeRevoke,
		Data:      data,
		Timestamp: revokeTime,
	}
	revoke.ID = revoke.generateID()
	revokePayload, err := msgpack.Marshal(&revoke)
	assert.NoError(t, err)
	revokeFrame, err := msgpack.Marshal(bob.createSignedContainer(typeUpdateDevice, revokePayload))
	assert.NoError(t, err)
	_, accepted = me.handleUpdateDevice(bobAddr, revokeFrame, false)
	assert.True(t, accepted)

	var bobRecord user
	assert.NoError(t, me.database.Preload("Devices.Signature").First(&bobRecord, "id = ?", bobID).Error)
	// The child deliberately precedes its parent in the carried record.
	bobRecord.Devices = append(bobRecord.Devices, descendant, omitted, listed)
	myRecord, ok := me.currentUser()
	assert.True(t, ok)
	offerBytes, err := msgpack.Marshal(bobRecord)
	assert.NoError(t, err)
	requesterBytes, err := msgpack.Marshal(myRecord)
	assert.NoError(t, err)
	offerHash := blake3.Sum256(offerBytes)
	requesterHash := blake3.Sum256(requesterBytes)
	au := addUser{
		Nonce:              newNonce(),
		Xor:                xor(bobID, me.currentUserID()),
		Timestamp:          now,
		OfferUser:          offerBytes,
		RequesterUser:      requesterBytes,
		OfferDevice:        listed.Address,
		RequesterDevice:    me.network.Address(),
		OfferSignature:     listedKey.sign(requesterHash[:]),
		RequesterSignature: me.network.Sign(offerHash[:]),
	}
	au.ID = au.generateID()
	payload, err := msgpack.Marshal(&au)
	assert.NoError(t, err)
	_, accepted = me.handleAddUser(bnc01NewOnion().addr(), payload, false)
	assert.True(t, accepted, "a retained device unknown to us can sign for the counterparty")

	_, exists := me.getDeviceFromAddress(listed.Address)
	assert.True(t, exists, "addUser can teach a counterparty device retained by the revoke")
	for _, d := range []device{omitted, descendant} {
		_, exists = me.getDeviceFromAddress(d.Address)
		assert.False(t, exists, "addUser cannot plant an omitted counterparty device or its descendants")
	}

	additional := introducedByBounce(bobID, bob, bnc01NewOnion(), now)
	bobRecord.Devices = append(bobRecord.Devices, additional)
	offerBytes, err = msgpack.Marshal(bobRecord)
	assert.NoError(t, err)
	offerHash = blake3.Sum256(offerBytes)
	au.Nonce = newNonce()
	au.OfferUser = offerBytes
	au.OfferDevice = omitted.Address
	au.OfferSignature = omittedKey.sign(requesterHash[:])
	au.RequesterSignature = me.network.Sign(offerHash[:])
	au.ID = au.generateID()
	payload, err = msgpack.Marshal(&au)
	assert.NoError(t, err)
	_, accepted = me.handleAddUser(bnc01NewOnion().addr(), payload, false)
	assert.False(t, accepted, "a counterparty device omitted by the revoke cannot authorize addUser")
	_, exists = me.getDeviceFromAddress(additional.Address)
	assert.True(t, exists, "a known counterparty's valid devices are kept even when addUser is rejected")
}

func TestAUserHasAtMostTheMaximumNumberOfDevices(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	aliceID := alice.currentUserID()
	growDeviceGroup(t, me, aliceID, alice, maximumDevicesPerUser-2)
	assert.Equal(t, maximumDevicesPerUser-1, deviceCount(t, me, aliceID))

	// The last device under the cap is learned, the one after it is not
	last := introducedByBounce(aliceID, alice, bnc01NewOnion(), time.Now().Unix())
	_, accepted := me.handleDevice(alice.network.Address(), last.getPayload(), false)
	assert.True(t, accepted)
	assert.Equal(t, maximumDevicesPerUser, deviceCount(t, me, aliceID))

	oneTooMany := introducedByBounce(aliceID, alice, bnc01NewOnion(), time.Now().Unix())
	_, accepted = me.handleDevice(alice.network.Address(), oneTooMany.getPayload(), false)
	assert.False(t, accepted)
	assert.Equal(t, maximumDevicesPerUser, deviceCount(t, me, aliceID))

	// A whole record is held to the same cap: Alice's full group is valid, with one more device it is not
	validator, counting := validatorFor(me)
	var aliceRecord user
	assert.NoError(t, me.database.Preload("Devices.Signature").First(&aliceRecord, "id = ?", aliceID).Error)
	started := time.Now()
	assert.True(t, validator.hasValidDeviceGroup(aliceRecord))
	t.Logf("validating a full device group of %d devices took %s and %d signature verifications",
		maximumDevicesPerUser, time.Since(started), counting.verifications.Load())
	aliceRecord.Devices = append(aliceRecord.Devices, oneTooMany)
	assert.False(t, validator.hasValidDeviceGroup(aliceRecord))
}

func TestWeStopPairingSyncDevicesAtTheMaximum(t *testing.T) {
	me := newBounceUser("Me")
	t.Cleanup(me.Shutdown)
	meID := me.currentUserID()
	growDeviceGroup(t, me, meID, me, maximumDevicesPerUser-1)
	assert.Equal(t, maximumDevicesPerUser, deviceCount(t, me, meID))

	// A new device with a valid, unexpired offer and a good signature would normally be paired
	assert.NoError(t, me.database.Create(&syncDeviceOffer{Timestamp: time.Now().Unix(), Secret: "full-house"}).Error)
	newDevice := bnc01NewOnion()
	request, err := msgpack.Marshal(&syncDeviceRequest{
		Secret:    "full-house",
		Signature: newDevice.sign([]byte(me.network.Address())),
	})
	assert.NoError(t, err)
	me.handleSyncDeviceRequest(newDevice.addr(), request, false)

	_, stored := me.getDeviceFromAddress(newDevice.addr())
	assert.False(t, stored, "we would have a device group peers refuse")
	assert.Equal(t, maximumDevicesPerUser, deviceCount(t, me, meID))
}

func TestAWholeRecordIsStillCheckedInFull(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	aliceID := alice.currentUserID()
	validator, _ := validatorFor(me)

	// Alice's original introduces a second device, which introduces a third.  As a record the chain is fine...
	second := bnc01NewOnion()
	third := bnc01NewOnion()
	secondDevice := introducedByBounce(aliceID, alice, second, 1000)
	thirdDevice := introducedBy(aliceID, second.addr(), second, third, 1100)
	var aliceRecord user
	assert.NoError(t, me.database.Preload("Devices.Signature").First(&aliceRecord, "id = ?", aliceID).Error)
	aliceRecord.Devices = append(aliceRecord.Devices, secondDevice, thirdDevice)
	assert.True(t, validator.hasValidDeviceGroup(aliceRecord))

	// ...but not when the link in the middle, which the third device's own introduction never touches, is forged
	secondDevice.Signature.SignatureOfNewDevice = bnc01NewOnion().sign([]byte(second.addr()))
	aliceRecord.Devices = aliceRecord.Devices[:1]
	aliceRecord.Devices = append(aliceRecord.Devices, secondDevice, thirdDevice)
	assert.False(t, validator.hasValidDeviceGroup(aliceRecord))

	// Checked one at a time in creation order, the forgery is caught at the second device, before the third is
	// ever considered on top of it
	assert.False(t, validator.isValidAddition(aliceRecord, secondDevice))
}

// A user record's Images field is stored exactly as a peer sent it and parsed later, so a bad value has to be refused
// at every ingress and again at the database, and a bad row that is already present must not take the process down.
func TestUserRecordImagesAreValidatedBeforeTheyAreStored(t *testing.T) {
	me, _, _, groupID := createUsersAndGroups(t)

	carol := newBounceUser("Carol")
	t.Cleanup(carol.Shutdown)
	carolUser, ok := carol.currentUser()
	assert.True(t, ok)
	var cu user
	cb, _ := msgpack.Marshal(carolUser)
	assert.NoError(t, msgpack.Unmarshal(cb, &cu))

	// The check every ingress runs: images, encrypted devices and name
	cu.Images = ""
	assert.NoError(t, validUserRecord(cu))
	cu.Images = NewID().String() + "," + NewID().String()
	assert.NoError(t, validUserRecord(cu))
	cu.Images = "not-an-id"
	assert.Error(t, validUserRecord(cu))
	cu.Images = NewID().String() + ",,"
	assert.Error(t, validUserRecord(cu))
	cu.Images = ""

	cu.EncryptedDevices = carol.network.Address() + "," + me.network.Address()
	assert.NoError(t, validUserRecord(cu))
	cu.EncryptedDevices = "not-an-address"
	assert.Error(t, validUserRecord(cu))
	cu.EncryptedDevices = carol.network.Address() + ","
	assert.Error(t, validUserRecord(cu))
	cu.EncryptedDevices = ""

	for _, bad := range []string{"", " padded", "two\nlines", strings.Repeat("x", MaximumNameLength+1)} {
		cu.Name = bad
		assert.Error(t, validUserRecord(cu), bad)
	}
	cu.Name = "Carol"
	assert.NoError(t, validUserRecord(cu))

	// Our own profile is held to the same rules, so a peer never rejects a record we made
	fresh := newBounce()
	t.Cleanup(fresh.Shutdown)
	assert.Error(t, fresh.SetProfile(" padded", []byte{}, "test"))
	assert.NoError(t, fresh.SetProfile("Fresh", []byte{}, "test"))

	// The invite path refuses to create the user, and nothing is written
	cu.Images = "not-an-id"
	assert.False(t, me.createNewUserIfNeeded(cu, groupID))
	var count int64
	assert.NoError(t, me.database.Model(&user{}).Where("id = ?", cu.ID).Count(&count).Error)
	assert.Equal(t, int64(0), count)

	// The database refuses the row even if a handler forgets to check
	assert.Error(t, me.database.Create(&cu).Error)

	// A corrupt row that is already present degrades to a skipped entry rather than a fatal error
	good := NewID()
	corrupt := user{ID: NewID(), Images: good.String() + ",garbage"}
	assert.Equal(t, []ID{good}, corrupt.images())
}
