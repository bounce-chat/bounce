package chat

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
	"github.com/google/uuid"
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
func introducedBy(owner uuid.UUID, introducer string, introducerKey *bnc01Onion, newDevice *bnc01Onion, timestamp int64) device {
	return device{
		ID:        uuid.New(),
		UserID:    owner,
		Address:   newDevice.addr(),
		Timestamp: timestamp,
		Signature: &introductionSignature{
			PreexistingDevice:            introducer,
			SignatureOfNewDevice:         introducerKey.sign([]byte(newDevice.addr())),
			SignatureOfPreexistingDevice: newDevice.sign([]byte(introducer)),
		},
	}
}

// introducedByBounce is introducedBy for an introducer that is a running engine, which signs with its real key
func introducedByBounce(owner uuid.UUID, introducer *Bounce, newDevice *bnc01Onion, timestamp int64) device {
	return device{
		ID:        uuid.New(),
		UserID:    owner,
		Address:   newDevice.addr(),
		Timestamp: timestamp,
		Signature: &introductionSignature{
			PreexistingDevice:            introducer.network.Address(),
			SignatureOfNewDevice:         introducer.network.Sign([]byte(newDevice.addr())),
			SignatureOfPreexistingDevice: newDevice.sign([]byte(introducer.network.Address())),
		},
	}
}

// growDeviceGroup stores count more devices for owner in b's database, each legitimately introduced by introducer,
// as if they had been learned one at a time
func growDeviceGroup(t *testing.T, b *Bounce, owner uuid.UUID, introducer *Bounce, count int) {
	t.Helper()
	added := make([]device, 0, count)
	for i := 0; i < count; i++ {
		added = append(added, introducedByBounce(owner, introducer, bnc01NewOnion(), time.Now().Unix()))
	}
	assert.NoError(t, b.database.CreateInBatches(&added, 100).Error)
}

func deviceCount(t *testing.T, b *Bounce, owner uuid.UUID) int {
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
	assert.False(t, validator.isValidAddition(aliceRecord, device{ID: uuid.New(), UserID: aliceID, Address: bnc01NewOnion().addr(), Timestamp: now}),
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

func TestARevokedDeviceCannotIntroduceNewOnes(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)
	aliceID := alice.currentUserID()
	validator, _ := validatorFor(me)

	// Alice's original introduces a second device, which then introduces a third, and the second is later revoked
	second := bnc01NewOnion()
	secondDevice := introducedByBounce(aliceID, alice, second, 1000)
	assert.NoError(t, me.database.Create(&secondDevice).Error)
	assert.NoError(t, me.database.Model(&device{}).Where("address = ?", second.addr()).Update("revoked_at", 2000).Error)
	var aliceRecord user
	assert.NoError(t, me.database.Preload("Devices.Signature").First(&aliceRecord, "id = ?", aliceID).Error)

	// A device the second introduced before it was revoked is still a valid addition, one after is not
	assert.True(t, validator.isValidAddition(aliceRecord, introducedBy(aliceID, second.addr(), second, bnc01NewOnion(), 1500)))
	assert.False(t, validator.isValidAddition(aliceRecord, introducedBy(aliceID, second.addr(), second, bnc01NewOnion(), 2500)))

	// A record that claims the second is not revoked does not get to override what we know
	for i := range aliceRecord.Devices {
		aliceRecord.Devices[i].RevokedAt = 0
	}
	assert.False(t, validator.isValidAddition(aliceRecord, introducedBy(aliceID, second.addr(), second, bnc01NewOnion(), 2500)))
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
