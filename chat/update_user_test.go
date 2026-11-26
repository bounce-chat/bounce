package chat

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
)

func makeValidKeySet(t *testing.T, kek []byte) []byte {
	t.Helper()
	pubECDSA, privECDSA, err := ed25519.GenerateKey(rand.Reader)
	assert.NoError(t, err)
	privECDH, err := ecdh.X25519().GenerateKey(rand.Reader)
	assert.NoError(t, err)
	ks := keySet{
		PrivateECDSAKey: privECDSA,
		PublicECDSAKey:  pubECDSA,
		PrivateECDHKey:  privECDH.Bytes(),
		PublicECDHKey:   privECDH.PublicKey().Bytes(),
		Kek:             kek,
	}
	data, err := msgpack.Marshal(&ks)
	assert.NoError(t, err)
	return data
}

func TestRemoteSyncScopedAndOversizedReplaceKeysAreRejected(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)

	// A contact must not be able to push sync-scoped updateUser types (ReplaceKeys, SetEncryptedDeviceName).
	replaceKeys := updateUser{
		Nonce:     newNonce(),
		Target:    alice.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeReplaceKeys,
		Data:      makeValidKeySet(t, make([]byte, 32)),
	}
	replaceKeys.ID = replaceKeys.generateID()
	br, accepted := me.handleUpdateUser(alice.network.Address(), signedFrame(t, alice, typeUpdateUser, replaceKeys), false)
	assert.False(t, accepted)
	assert.True(t, br == nil)

	setDeviceName := updateUser{
		Nonce:     newNonce(),
		Target:    alice.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeSetEncryptedDeviceName,
		Data:      []byte(alice.network.Address() + ":phone"),
	}
	setDeviceName.ID = setDeviceName.generateID()
	br, accepted = me.handleUpdateUser(alice.network.Address(), signedFrame(t, alice, typeUpdateUser, setDeviceName), false)
	assert.False(t, accepted)
	assert.True(t, br == nil)

	var syncCount int64
	assert.NoError(t, me.database.Model(&updateUser{}).
		Where("target = ? AND type IN ?", alice.currentUserID(), []uint16{updateUserTypeReplaceKeys, updateUserTypeSetEncryptedDeviceName}).
		Count(&syncCount).Error)
	assert.Equal(t, int64(0), syncCount)

	// ReplaceKeys with a bad Kek size or bloated Data must fail validation even for our own user.
	badKek := updateUser{
		Nonce:     newNonce(),
		Target:    me.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeReplaceKeys,
		Data:      makeValidKeySet(t, make([]byte, 64)),
	}
	badKek.ID = badKek.generateID()
	assert.True(t, errors.Is(badKek.validPayload(), errInvalidKeyMaterial))

	hugeKek := updateUser{
		Nonce:     newNonce(),
		Target:    me.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeReplaceKeys,
		Data:      makeValidKeySet(t, make([]byte, 128*1024)),
	}
	hugeKek.ID = hugeKek.generateID()
	assert.True(t, errors.Is(hugeKek.validPayload(), errInvalidKeyMaterial))
	br, accepted = me.handleUpdateUser(me.network.Address(), signedFrame(t, me, typeUpdateUser, hugeKek), false)
	assert.False(t, accepted)
	assert.True(t, br == nil)

	// The wire reader itself must reject an oversized updateUser header before allocating/reading it.
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	go func() {
		header := make([]byte, headerSize)
		binary.BigEndian.PutUint16(header[:typeSize], typeUpdateUser)
		binary.BigEndian.PutUint32(header[typeSize:], maximumSmallControlFramePayload+1)
		_, _ = client.Write(header)
	}()
	_, _, err := readFrame(server, true)
	assert.True(t, errors.Is(err, errFrameTypePayloadTooLarge))
}

func TestUpdateUserHistoryIsPrunedAndStaleFramesSkipped(t *testing.T) {
	me, alice, _, _ := createUsersAndGroups(t)

	extra := 25
	sent := make([]ID, 0, maximumUpdateUserHistoryPerType+extra)
	for i := 0; i < maximumUpdateUserHistoryPerType+extra; i++ {
		uu := updateUser{
			Nonce:     newNonce(),
			Target:    alice.currentUserID(),
			Timestamp: int64(1_000_000 + i),
			Type:      updateUserTypeUpdateName,
			Data:      []byte(fmt.Sprintf("Alice-%d", i)),
		}
		uu.ID = uu.generateID()
		sent = append(sent, uu.ID)
		_, accepted := me.handleUpdateUser(alice.network.Address(), signedFrame(t, alice, typeUpdateUser, uu), true)
		assert.True(t, accepted)
	}
	me.updateUserState(alice.currentUserID())

	// Exactly maximumUpdateUserHistoryPerType rows remain, and they are the newest ones.
	var kept []updateUser
	assert.NoError(t, me.database.
		Where("target = ? AND type = ?", alice.currentUserID(), updateUserTypeUpdateName).
		Order("timestamp ASC, id ASC").
		Find(&kept).Error)
	assert.Equal(t, maximumUpdateUserHistoryPerType, len(kept))
	assert.Equal(t, sent[extra], kept[0].ID)
	assert.Equal(t, sent[len(sent)-1], kept[len(kept)-1].ID)

	var u user
	assert.NoError(t, me.database.Where("id = ?", alice.currentUserID()).First(&u).Error)
	assert.Equal(t, fmt.Sprintf("Alice-%d", maximumUpdateUserHistoryPerType+extra-1), u.Name)

	// A stale/backdated frame older than the oldest kept entry is skipped up front.
	stale := updateUser{
		Nonce:     newNonce(),
		Target:    alice.currentUserID(),
		Timestamp: 999_999,
		Type:      updateUserTypeUpdateName,
		Data:      []byte("OldAlice"),
	}
	stale.ID = stale.generateID()
	br, accepted := me.handleUpdateUser(alice.network.Address(), signedFrame(t, alice, typeUpdateUser, stale), true)
	assert.False(t, accepted)
	assert.True(t, br == nil)

	// Pruning a row cascades to its delivery records via AfterDelete.
	oldestKeptID := kept[0].ID
	assert.NoError(t, me.database.Create(&deliveryRecord{
		FrameID:     oldestKeptID,
		FrameType:   typeUpdateUser,
		Destination: "peer-delivery-check",
	}).Error)
	newest := updateUser{
		Nonce:     newNonce(),
		Target:    alice.currentUserID(),
		Timestamp: int64(1_000_000 + maximumUpdateUserHistoryPerType + extra),
		Type:      updateUserTypeUpdateName,
		Data:      []byte("Alice-newest"),
	}
	newest.ID = newest.generateID()
	_, accepted = me.handleUpdateUser(alice.network.Address(), signedFrame(t, alice, typeUpdateUser, newest), true)
	assert.True(t, accepted)

	var remainingDRs int64
	assert.NoError(t, me.database.Model(&deliveryRecord{}).
		Where("frame_id = ? AND frame_type = ?", oldestKeptID, typeUpdateUser).
		Count(&remainingDRs).Error)
	assert.Equal(t, int64(0), remainingDRs)

	// Another update type (UpdateImage) has its own independent 100-row budget.
	imgID := NewID()
	imgUpdate := updateUser{
		Nonce:     newNonce(),
		Target:    alice.currentUserID(),
		Timestamp: time.Now().Unix(),
		Type:      updateUserTypeUpdateImage,
		Data:      imgID[:],
	}
	imgUpdate.ID = imgUpdate.generateID()
	_, accepted = me.handleUpdateUser(alice.network.Address(), signedFrame(t, alice, typeUpdateUser, imgUpdate), true)
	assert.True(t, accepted)
}

func TestLiveUpdateUserRateLimitSkipsCatchUp(t *testing.T) {
	origCount := updateUserRateLimitCount
	origWindow := updateUserRateLimitWindow
	updateUserRateLimitCount = 5
	updateUserRateLimitWindow = 100 * time.Millisecond
	t.Cleanup(func() {
		updateUserRateLimitCount = origCount
		updateUserRateLimitWindow = origWindow
	})

	me, alice, _, _ := createUsersAndGroups(t)

	// First 5 live updates succeed; subsequent live updates within the window are rate-limited.
	for i := 0; i < 8; i++ {
		uu := updateUser{
			Nonce:     newNonce(),
			Target:    alice.currentUserID(),
			Timestamp: int64(2_000_000 + i),
			Type:      updateUserTypeUpdateName,
			Data:      []byte(fmt.Sprintf("Live-%d", i)),
		}
		uu.ID = uu.generateID()
		br, accepted := me.handleUpdateUser(alice.network.Address(), signedFrame(t, alice, typeUpdateUser, uu), false)
		if i < 5 {
			assert.True(t, accepted)
			assert.True(t, br != nil)
		} else {
			assert.False(t, accepted)
			assert.True(t, br == nil)
		}
	}

	// Catch-up updates bypass the live rate limit so offline devices can process history quickly.
	for i := 0; i < 3; i++ {
		uu := updateUser{
			Nonce:     newNonce(),
			Target:    alice.currentUserID(),
			Timestamp: int64(2_000_100 + i),
			Type:      updateUserTypeUpdateName,
			Data:      []byte(fmt.Sprintf("CatchUp-%d", i)),
		}
		uu.ID = uu.generateID()
		br, accepted := me.handleUpdateUser(alice.network.Address(), signedFrame(t, alice, typeUpdateUser, uu), true)
		assert.True(t, accepted)
		assert.True(t, br != nil)
	}

	// Once the rate-limit window elapses, live updates are accepted again.
	time.Sleep(150 * time.Millisecond)
	after := updateUser{
		Nonce:     newNonce(),
		Target:    alice.currentUserID(),
		Timestamp: 2_000_200,
		Type:      updateUserTypeUpdateName,
		Data:      []byte("Live-After"),
	}
	after.ID = after.generateID()
	br, accepted := me.handleUpdateUser(alice.network.Address(), signedFrame(t, alice, typeUpdateUser, after), false)
	assert.True(t, accepted)
	assert.True(t, br != nil)
}
