package chat

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Basekick-Labs/msgpack/v6"
	"github.com/alecthomas/assert/v2"
	"github.com/cretz/bine/torutil"
	"github.com/cretz/bine/torutil/ed25519"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
)

var hosts = map[string]*testnet{}

var networkTracking sync.Mutex
var networkWaiting = map[string]chan bool{}
var broadcast = map[string][]frameReference{}
var acked = map[string][]frameReference{}

type testnet struct {
	publicKey  ed25519.PublicKey
	privateKey ed25519.PrivateKey
	incoming   chan net.Conn
}

func newTestnet() *testnet {
	keypair, _ := ed25519.GenerateKey(rand.Reader)
	t := &testnet{
		publicKey:  keypair.PublicKey(),
		privateKey: keypair.PrivateKey(),
		incoming:   make(chan net.Conn),
	}
	hosts[t.Address()] = t
	return t
}

func (t *testnet) Load(_ string) {}

func (t *testnet) Start(callbacks NetworkCallbacks) {
	callbacks.NetworkOnline()
	select {}
}

func (t *testnet) Address() string {
	return torutil.OnionServiceIDFromV3PublicKey(t.publicKey)
}

func (t *testnet) Accept() (conn net.Conn, err error, fatal bool) {
	c := <-t.incoming
	return c, nil, false
}

func (t *testnet) Dial(address string) (net.Conn, error) {
	host, ok := hosts[address]
	if !ok {
		return nil, errors.New("address not found in testnet")
	}

	dialerUnderlying, dialerIntercept := net.Pipe()
	listenerUnderlying, listenerIntercept := net.Pipe()

	go func() {
		for {
			frameType, payload, err := readFrame(dialerIntercept, true)
			if err != nil {
				return
			}
			err = writeFrame(listenerIntercept, frameType, payload)
			if err != nil {
				return
			}
			written(t.Address(), address, frameType, payload)
		}
	}()
	go func() {
		for {
			frameType, payload, err := readFrame(listenerIntercept, true)
			if err != nil {
				return
			}
			err = writeFrame(dialerIntercept, frameType, payload)
			if err != nil {
				return
			}
			written(address, t.Address(), frameType, payload)
		}
	}()

	dialer := &testnetConnection{
		underlying: dialerUnderlying,
		localAddress: &testnetAddress{
			address: t.Address(),
		},
		remoteAddress: &testnetAddress{
			address: address,
		},
	}
	listener := &testnetConnection{
		underlying: listenerUnderlying,
		localAddress: &testnetAddress{
			address: address,
		},
		remoteAddress: &testnetAddress{
			address: t.Address(),
		},
	}

	host.incoming <- listener
	return dialer, nil
}

func (t *testnet) Sign(data []byte) []byte {
	return ed25519.Sign(t.privateKey, data)
}

func (t *testnet) VerifySignature(address string, data []byte, signature []byte) bool {
	publicKey, err := torutil.PublicKeyFromV3OnionServiceID(address)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("invalid address passed to VerifySignature")
		return false
	}
	return ed25519.Verify(publicKey, data, signature)
}

func (t testnet) Shutdown() {}

type testnetConnection struct {
	underlying    net.Conn
	localAddress  net.Addr
	remoteAddress net.Addr
}

func (conn *testnetConnection) Read(b []byte) (int, error) {
	return conn.underlying.Read(b)
}

func (conn *testnetConnection) Write(b []byte) (int, error) {
	return conn.underlying.Write(b)
}

func (conn *testnetConnection) Close() error {
	return conn.underlying.Close()
}

func (conn *testnetConnection) LocalAddr() net.Addr {
	return conn.localAddress
}

func (conn *testnetConnection) RemoteAddr() net.Addr {
	return conn.remoteAddress
}

func (conn *testnetConnection) SetDeadline(t time.Time) error {
	return conn.underlying.SetDeadline(t)
}

func (conn *testnetConnection) SetReadDeadline(t time.Time) error {
	return conn.underlying.SetReadDeadline(t)
}

func (conn *testnetConnection) SetWriteDeadline(t time.Time) error {
	return conn.underlying.SetWriteDeadline(t)
}

type testnetAddress struct {
	address string
}

func (ta *testnetAddress) Network() string {
	return "testnet"
}

func (ta *testnetAddress) String() string {
	return ta.address
}

func written(from, to string, frameType uint16, payload []byte) {
	//id, track, err := getID(frameType, payload)
	//if err != nil {
	//	log.Error(err.Error())
	//	return
	//}

	//if track {
	//	flow := from + to
	//	frameReference{
	//		Type:    frameType,
	//		FrameID: id,
	//	}
	//	broadcast[flow] = append(broadcast[flow], frameReference)
	//	// TODO write any waiters expecting this reference on this flow
	//}

	//if frameType == typeCatchUp {
	//	// TODO: recurse
	//}

	if frameType == typeAck {
		var a ack
		err := msgpack.Unmarshal(payload, &a)
		if err != nil {
			log.Error(err.Error())
			return
		}
		for _, ref := range a.References {
			flow := from + to
			networkTracking.Lock()
			acked[flow] = append(acked[flow], ref)
			signature := flow + strconv.Itoa(int(ref.Type)) + ref.FrameID.String()
			if waiter, ok := networkWaiting[signature]; ok {
				waiter <- true
				delete(networkWaiting, signature)
			}
			networkTracking.Unlock()
		}
	}
}

func awaitAck(t *testing.T, to, from *Bounce, frameType uint16, frameID uuid.UUID) {
	flow := from.network.Address() + to.network.Address()
	networkTracking.Lock()
	refs := acked[flow]
	for _, ref := range refs {
		if ref.FrameID == frameID && ref.Type == frameType {
			networkTracking.Unlock()
			return
		}
	}

	signature := flow + strconv.Itoa(int(frameType)) + frameID.String()
	if _, ok := networkWaiting[signature]; ok {
		log.Fatal("already awaiting " + signature)
	}

	waiter := make(chan bool, 1)
	networkWaiting[signature] = waiter
	networkTracking.Unlock()

	select {
	case <-waiter:
	case <-time.After(2 * time.Second):
		details := ""
		_, file, no, ok := runtime.Caller(1)
		if ok {
			details = fmt.Sprintf(", from %s:%d", file, no)
		}
		t.Fatal("timeout waiting for network" + details)
	}
}

/*
func getID(frameType uint16, payload []byte) (uuid.UUID, bool, error) {
	switch frameType {
	case typeDirectMessage:
		var dm directMessage
		err := msgpack.Unmarshal(payload, &dm)
		if err != nil {
			return uuid.Nil, true, err
		}
		return dm.getID(), true, nil
	case typeGroupMessage:
		var gm groupMessage
		err := msgpack.Unmarshal(payload, &gm)
		if err != nil {
			return uuid.Nil, true, err
		}
		return gm.getID(), true, nil
	case typeReferenceOffer:
		return uuid.Nil, false, nil
	case typeReferenceRequest:
		return uuid.Nil, false, nil
	case typeCatchUp:
		return uuid.Nil, false, nil
	case typeAck:
		return uuid.Nil, false, nil
	case typeKeepAlive:
		return uuid.Nil, false, nil
	case typeSyncDeviceRequest:
	case typeSyncDeviceRequestRejected:
	case typeSyncDeviceRequestAccepted:
	case typeDevice:
	case typeUpdateDM:
	case typeGroupCreation:
	case typeUpdateGroup:
	case typeTypingIndicator:
	case typeAddUserRequest:
	case typeAddUserRequestAccepted:
	case typeAddUserRequestRejected:
	case typeAddUser:
	case typeConfirmation:
	case typeUpdateUser:
	case typeUpdateDevice:
	case typeReadReceipt:
	case typeUpdateSettings:
	}
	return uuid.Nil, false, errors.New("unknown frame type")
}
*/

type call struct {
	function string
	args     []interface{}
}

type testUI struct {
	sync.Mutex

	bounce  *Bounce
	closer  chan bool
	called  chan call
	calls   []call
	waiting map[string]chan bool
}

func newTestUI() *testUI {
	ui := &testUI{
		closer:  make(chan bool),
		called:  make(chan call, 100),
		calls:   []call{},
		waiting: make(map[string]chan bool),
	}

	go func() {
		for {
			c := <-ui.called

			ui.Lock()
			if waiter, ok := ui.waiting[c.function]; ok { // TODO: match args
				waiter <- true
				delete(ui.waiting, c.function)
			}
			ui.calls = append(ui.calls, c)
			ui.Unlock()
		}
	}()

	return ui
}

func await(t *testing.T, b *Bounce, function string, args ...interface{}) {
	ui := b.ui.(*testUI)

	ui.Lock()
	for _, c := range ui.calls {
		// TODO: match args
		if c.function == function {
			ui.Unlock()
			return
		}
	}

	if _, ok := ui.waiting[function]; ok {
		log.Fatal("already awaiting " + function)
	}

	waiter := make(chan bool)
	ui.waiting[function] = waiter
	ui.Unlock()

	select {
	case <-waiter:
	case <-time.After(2 * time.Second):
		details := ""
		_, file, no, ok := runtime.Caller(1)
		if ok {
			details = fmt.Sprintf(", from %s:%d", file, no)
		}
		t.Fatal("timeout waiting for UI" + details)
	}
}

func firstAddress(b *Bounce) string {
	u, _ := b.currentUser()
	return u.Devices[0].Address
}

func (t *testUI) LoadInitialState(InitialState) {}

func (t *testUI) Run() {
	<-t.closer
}

func (t *testUI) Quit() {
	t.closer <- true
}

func (t *testUI) NetworkOnline()                                 {}
func (t *testUI) NetworkOffline()                                {}
func (t *testUI) NewSyncDeviceAdded()                            {}
func (t *testUI) SyncDeviceRequestAccepted(User, []Device, bool) {}
func (t *testUI) SyncDeviceRequestRejected(peer string)          {}
func (t *testUI) ProfileSet(User, Device)                        {}
func (t *testUI) InitialSyncStarting()                           {}
func (t *testUI) InitialSyncProgress(float64)                    {}
func (t *testUI) InitialSyncPreparing()                          {}
func (t *testUI) InitialSyncComplete()                           {}
func (t *testUI) AddUserRequestRejected(string)                  {}
func (t *testUI) UserAdded(u User) {
	t.called <- call{function: "UserAdded", args: []interface{}{u}}
}
func (t *testUI) DeleteItem(uuid.UUID)                      {}
func (t *testUI) MarkMessageUndeliverable(uuid.UUID)        {}
func (t *testUI) DisplayDirectMessage(DirectMessage)        {}
func (t *testUI) DisplaySentDirectMessage(DirectMessage)    {}
func (t *testUI) SetDMState(uuid.UUID, DMState)             {}
func (t *testUI) DMRetentionChanged(UpdateDMRetention)      {}
func (t *testUI) DMChatHistoryCleared(UpdateDMClearHistory) {}
func (t *testUI) UserAliased(UpdateDMSetAlias)              {}
func (t *testUI) SetUserName(uuid.UUID, string)             {}
func (t *testUI) UserNameUpdated(UpdateUserUpdateName)      {}
func (t *testUI) OpenNewGroupChat(Group)                    {}
func (t *testUI) NewGroupChat(g Group) {
	t.called <- call{function: "NewGroupChat", args: []interface{}{g}}
}
func (t *testUI) SetGroupState(g Group) {
	t.called <- call{function: "SetGroupState", args: []interface{}{g}}
}
func (t *testUI) DisplaySentGroupMessage(GroupMessage) {}
func (t *testUI) DisplayGroupMessage(GroupMessage)     {}
func (t *testUI) RemoveUser(ugru UpdateGroupRemoveUser) {
	t.called <- call{function: "RemoveUser", args: []interface{}{ugru}}
}
func (t *testUI) RemovedFromGroup(RemovedFromGroup) {}
func (t *testUI) GroupDeleted(GroupDeleted)         {}
func (t *testUI) UserBlockedGroup(UserBlockedGroup) {}
func (t *testUI) RenameGroup(u UpdateGroupName) {
	t.called <- call{function: "RenameGroup", args: []interface{}{u}}
}
func (t *testUI) GroupRetentionChanged(UpdateGroupRetention)                       {}
func (t *testUI) GroupChatHistoryCleared(UpdateGroupClearHistory)                  {}
func (t *testUI) AdminPromoted(UpdateGroupAdminPromoted)                           {}
func (t *testUI) AdminDemoted(UpdateGroupAdminDemoted)                             {}
func (t *testUI) UserManagementRestricted(UpdateGroupUserManagementRestricted)     {}
func (t *testUI) UserManagementUnrestricted(UpdateGroupUserManagementUnrestricted) {}
func (t *testUI) GroupEditsRestricted(UpdateGroupEditsRestricted)                  {}
func (t *testUI) GroupEditsUnrestricted(UpdateGroupEditsUnrestricted)              {}
func (t *testUI) PostingRestricted(UpdateGroupPostingRestricted)                   {}
func (t *testUI) PostingUnrestricted(UpdateGroupPostingUnrestricted)               {}
func (t *testUI) InviteUser(UpdateGroupInviteUser)                                 {}
func (t *testUI) RollbackGroup(uuid.UUID)                                          {}
func (t *testUI) GroupInviteRevoked(UpdateGroupInviteRevoked)                      {}
func (t *testUI) GroupInviteAccepted(UpdateGroupInviteAccepted)                    {}
func (t *testUI) GroupInviteRejected(UpdateGroupInviteRejected)                    {}
func (t *testUI) ShowTypingIndicator(userID, threadID uuid.UUID)                   {}
func (t *testUI) HideTypingIndicator(userID, threadID uuid.UUID)                   {}
func (t *testUI) UserOnline(userID uuid.UUID)                                      {}
func (t *testUI) UserOffline(userID uuid.UUID)                                     {}
func (t *testUI) DeviceOnline(uuid.UUID)                                           {}
func (t *testUI) DeviceOffline(uuid.UUID)                                          {}
func (t *testUI) DeviceAdded(Device)                                               {}
func (t *testUI) DeviceRevoked(uuid.UUID)                                          {}
func (t *testUI) DeviceRenamed(uuid.UUID, string)                                  {}
func (t *testUI) DeviceLastSeen(uuid.UUID, int64)                                  {}
func (t *testUI) MessageSeen(uuid.UUID)                                            {}
func (t *testUI) ReceivedReadReceipt(ReadReceipt)                                  {}
func (t *testUI) SetSettings(Settings)                                             {}
func (t *testUI) MessageDelivered(messageID, userID uuid.UUID)                     {}
func (t *testUI) SetDarkMode(value bool)                                           {}
func (t *testUI) SetUserState(User)                                                {}
func (t *testUI) FileCompleted(uuid.UUID)                                          {}
func (t *testUI) UserChangedGroupImage(UpdateGroupUserChangedGroupImage)           {}
func (t *testUI) UserImageUpdated(UpdateUserUpdateImage)                           {}
func (t *testUI) FileDownloadProgress(uuid.UUID, float64)                          {}
func (t *testUI) CatchUpMessages(BulkUpdate, bool)                                 {}
func (t *testUI) AnotherDeviceActive()                                             {}
func (t *testUI) NoOtherDeviceActive()                                             {}
func (t *testUI) EncryptedDeviceAdded()                                            {}
func (t *testUI) EncryptedDeviceRejected()                                         {}
func (t *testUI) EncryptedDeviceManagable(uuid.UUID)                               {}
func (t *testUI) EncryptedDeviceUnmanagable(uuid.UUID)                             {}
func (t *testUI) UpdateDraft(Draft)                                                {}

func newBounce() *Bounce {
	ui := newTestUI()
	network := newTestnet()
	configDirectory := os.TempDir() + "/bounce-test-" + uuid.New().String()
	os.MkdirAll(configDirectory, 0700)
	os.MkdirAll(configDirectory+"/blobs/", 0700)

	return Open(ui, network, configDirectory, nil, nil).(*Bounce)
}

func newBounceUser(name string) *Bounce {
	b := newBounce()
	b.SetProfile(name, []byte{}, "test")
	return b
}

func createUsersAndGroups(t *testing.T) (me, alice, bob *Bounce, groupID uuid.UUID) {
	me = newBounceUser("Me")
	meUser, ok := me.currentUser()
	assert.True(t, ok)

	alice = newBounceUser("Alice")
	aliceUser, ok := alice.currentUser()
	assert.True(t, ok)

	bob = newBounceUser("Bob")
	bobUser, ok := bob.currentUser()
	assert.True(t, ok)

	var mu user
	mb, _ := msgpack.Marshal(meUser)
	msgpack.Unmarshal(mb, &mu)
	var au user
	ab, _ := msgpack.Marshal(aliceUser)
	msgpack.Unmarshal(ab, &au)
	var bu user
	bb, _ := msgpack.Marshal(bobUser)
	msgpack.Unmarshal(bb, &bu)

	assert.NoError(t, me.database.Create(&au).Error)
	assert.NoError(t, me.database.Create(&bu).Error)
	assert.NoError(t, alice.database.Create(&mu).Error)
	assert.NoError(t, alice.database.Create(&bu).Error)
	assert.NoError(t, bob.database.Create(&mu).Error)
	assert.NoError(t, bob.database.Create(&au).Error)

	me.UserConnectionDesired(aliceUser.ID)
	me.UserConnectionDesired(bobUser.ID)
	alice.UserConnectionDesired(meUser.ID)
	alice.UserConnectionDesired(bobUser.ID)
	bob.UserConnectionDesired(meUser.ID)
	bob.UserConnectionDesired(aliceUser.ID)

	gc := me.createGroupCreation(NewGroup{
		Name: "Test Group",
	})
	groupID = gc.ID
	me.database.Create(&gc)
	alice.database.Create(&gc)
	bob.database.Create(&gc)

	inviteTime := time.Now().Unix() - 2
	acceptTime := inviteTime + 1
	var err error

	addAlice := &updateGroup{
		ID:        uuid.New(),
		Actor:     me.currentUserID(),
		Target:    groupID,
		Timestamp: inviteTime,
		Type:      updateGroupTypeInviteUser,
		Data:      ab,
	}
	addAlice.OriginalPayload, err = msgpack.Marshal(addAlice)
	assert.NoError(t, err)
	sc := me.createSignedContainer(addAlice.OriginalPayload)
	addAlice.Signature = sc.Signature
	addAlice.Signer = sc.Signer

	addBob := &updateGroup{
		ID:        uuid.New(),
		Actor:     me.currentUserID(),
		Target:    groupID,
		Timestamp: inviteTime,
		Type:      updateGroupTypeInviteUser,
		Data:      bb,
	}
	addBob.OriginalPayload, err = msgpack.Marshal(addBob)
	assert.NoError(t, err)
	sc = me.createSignedContainer(addBob.OriginalPayload)
	addBob.Signature = sc.Signature
	addBob.Signer = sc.Signer

	aliceAccepts := &updateGroup{
		ID:        uuid.New(),
		Actor:     alice.currentUserID(),
		Target:    groupID,
		Timestamp: acceptTime,
		Type:      updateGroupTypeRespondToInvite,
		Data:      []byte{acceptInvite},
	}
	aliceAccepts.OriginalPayload, err = msgpack.Marshal(aliceAccepts)
	assert.NoError(t, err)
	sc = alice.createSignedContainer(aliceAccepts.OriginalPayload)
	aliceAccepts.Signature = sc.Signature
	aliceAccepts.Signer = sc.Signer

	bobAccepts := &updateGroup{
		ID:        uuid.New(),
		Actor:     bob.currentUserID(),
		Target:    groupID,
		Timestamp: acceptTime,
		Type:      updateGroupTypeRespondToInvite,
		Data:      []byte{acceptInvite},
	}
	bobAccepts.OriginalPayload, err = msgpack.Marshal(bobAccepts)
	assert.NoError(t, err)
	sc = bob.createSignedContainer(bobAccepts.OriginalPayload)
	bobAccepts.Signature = sc.Signature
	bobAccepts.Signer = sc.Signer

	assert.NoError(t, me.database.Create(addAlice).Error)
	assert.NoError(t, me.database.Create(addBob).Error)
	assert.NoError(t, me.database.Create(aliceAccepts).Error)
	assert.NoError(t, me.database.Create(bobAccepts).Error)
	assert.NoError(t, alice.database.Create(addAlice).Error)
	assert.NoError(t, alice.database.Create(addBob).Error)
	assert.NoError(t, alice.database.Create(aliceAccepts).Error)
	assert.NoError(t, alice.database.Create(bobAccepts).Error)
	assert.NoError(t, bob.database.Create(addAlice).Error)
	assert.NoError(t, bob.database.Create(addBob).Error)
	assert.NoError(t, bob.database.Create(aliceAccepts).Error)
	assert.NoError(t, bob.database.Create(bobAccepts).Error)

	me.reloadGroupConsensus(groupID)
	me.writeGroupConsensus(groupID)
	alice.reloadGroupConsensus(groupID)
	alice.writeGroupConsensus(groupID)
	bob.reloadGroupConsensus(groupID)
	bob.writeGroupConsensus(groupID)

	t.Cleanup(func() {
		me.Shutdown()
		alice.Shutdown()
		bob.Shutdown()
		os.RemoveAll(me.configDirectory)
		os.RemoveAll(alice.configDirectory)
		os.RemoveAll(bob.configDirectory)
	})

	me.ui.(*testUI).Lock()
	me.ui.(*testUI).calls = []call{}
	me.ui.(*testUI).Unlock()
	alice.ui.(*testUI).Lock()
	alice.ui.(*testUI).calls = []call{}
	alice.ui.(*testUI).Unlock()
	bob.ui.(*testUI).Lock()
	bob.ui.(*testUI).calls = []call{}
	bob.ui.(*testUI).Unlock()

	return
}

func (b *Bounce) createGroupCreation(ng NewGroup) groupCreation {
	profile, _ := b.currentUser()
	creationTime := time.Now().Unix() - 3 // Subtract one second to ensure any invites are ordered after the group creation
	g := group{
		ID:                     uuid.Nil,
		Name:                   ng.Name,
		Images:                 "",
		CreatedBy:              b.currentUserID(),
		CreatedAt:              creationTime,
		Retention:              ng.Retention,
		Users:                  []user{profile},
		Admins:                 b.currentUserID().String(),
		RestrictUserManagement: ng.RestrictUserManagement,
		RestrictGroupEdits:     ng.RestrictGroupEdits,
		RestrictPosting:        ng.RestrictPosting,
		LastActivity:           time.Now().Unix(),
	}

	groupData, err := msgpack.Marshal(g)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot msgpack marshal group")
	}

	hasher := blake3.New()
	written, _ := hasher.Write(groupData)
	if written != len(groupData) {
		log.WithFields(log.Fields{
			"length":  len(groupData),
			"written": written,
		}).Fatal("failed to write all group data into hasher")
	}
	digest := hasher.Digest()
	groupHash := make([]byte, 16)
	digest.Read(groupHash)
	groupID, err := uuid.FromBytes(groupHash)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("cannot create UUID from hash of group data")
	}
	g.ID = groupID

	gc := groupCreation{
		ID:        groupID,
		Timestamp: creationTime,
		Data:      groupData,
	}
	gc.OriginalPayload, err = msgpack.Marshal(gc)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error marshalling group creation")
	}
	sc := b.createSignedContainer(gc.OriginalPayload)
	gc.Signature = sc.Signature
	gc.Signer = sc.Signer

	return gc
}
