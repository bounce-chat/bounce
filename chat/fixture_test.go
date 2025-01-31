package chat

import (
	"crypto/rand"
	"errors"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/alecthomas/assert/v2"
	"github.com/cretz/bine/torutil"
	"github.com/cretz/bine/torutil/ed25519"
	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

var hosts = map[string]*testnet{}

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

	dialerUnderlying, listenerUnderlying := net.Pipe()
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

type call struct {
	function string
	args     []interface{}
}

type testUI struct {
	sync.Mutex

	callbacks UICallbacks
	closer    chan bool
	called    chan call
	calls     []call
	waiting   map[string]chan bool
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

func await(b *bounce, function string, args ...interface{}) {
	ui := b.userInterface.(*testUI)

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

	<-waiter
}

// TODO: get this off the wire to avoid sleeps
func awaitDelivery(t *testing.T, b *bounce, frameType uint16, frameID uuid.UUID) {
	for range 20 {
		var dr deliveryRecord
		err := b.database.First(&dr, "frame_type = ? AND frame_id = ?", frameType, frameID).Error
		if err == nil {
			return
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatal("timeout waiting for ack")
}

func (t *testUI) Build(configPath string, callbacks UICallbacks) {
	t.callbacks = callbacks
}

func (t *testUI) LoadInitialState(InitialState) {}

func (t *testUI) Run() {
	<-t.closer
}

func (t *testUI) Quit() {
	t.closer <- true
}

func (t *testUI) NetworkOnline()                                              {}
func (t *testUI) NetworkOffline()                                             {}
func (t *testUI) NewSyncDeviceAdded()                                         {}
func (t *testUI) SyncDeviceRequestAccepted(uuid.UUID, string, []Device, bool) {}
func (t *testUI) SyncDeviceRequestRejected(peer string)                       {}
func (t *testUI) InitialSyncStarting()                                        {}
func (t *testUI) InitialSyncProgress(float64)                                 {}
func (t *testUI) InitialSyncComplete()                                        {}
func (t *testUI) AddUserRequestRejected(string)                               {}
func (t *testUI) FriendAdded(User)                                            {}
func (t *testUI) UserImported(User)                                           {}
func (t *testUI) DeleteItem(uuid.UUID)                                        {}
func (t *testUI) MarkMessageUndeliverable(uuid.UUID)                          {}
func (t *testUI) DisplayDirectMessage(DirectMessage)                          {}
func (t *testUI) SetDMState(uuid.UUID, DMState)                               {}
func (t *testUI) DMRetentionChanged(UpdateDMRetention)                        {}
func (t *testUI) DMChatHistoryCleared(UpdateDMClearHistory)                   {}
func (t *testUI) SetUserName(uuid.UUID, string)                               {}
func (t *testUI) UserNameUpdated(UpdateUserUpdateName)                        {}
func (t *testUI) OpenNewGroupChat(Group)                                      {}
func (t *testUI) NewGroupChat(g Group) {
	t.called <- call{function: "NewGroupChat", args: []interface{}{g}}
}
func (t *testUI) SetGroupState(g Group) {
	t.called <- call{function: "SetGroupState", args: []interface{}{g}}
}
func (t *testUI) DisplayGroupMessage(GroupMessage)  {}
func (t *testUI) AddUser(UpdateGroupAddUser)        {}
func (t *testUI) RemoveUser(UpdateGroupRemoveUser)  {}
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
func (t *testUI) ShowTypingIndicatorInHistory(userID, threadID uuid.UUID)          {}
func (t *testUI) ShowTypingIndicatorInButton(userID, threadID uuid.UUID)           {}
func (t *testUI) HideTypingIndicatorInHistory(userID, threadID uuid.UUID)          {}
func (t *testUI) HideTypingIndicatorInButton(threadID uuid.UUID)                   {}
func (t *testUI) UserIsOnline(userID uuid.UUID)                                    {}
func (t *testUI) UserIsOffline(userID uuid.UUID)                                   {}
func (t *testUI) DeviceOnline(uuid.UUID)                                           {}
func (t *testUI) DeviceOffline(uuid.UUID)                                          {}
func (t *testUI) DeviceAdded(Device)                                               {}
func (t *testUI) DeviceRevoked(uuid.UUID)                                          {}
func (t *testUI) DeviceRenamed(uuid.UUID, string)                                  {}
func (t *testUI) MessageSeen(uuid.UUID)                                            {}
func (t *testUI) ReceivedReadReceipt(ReadReceipt)                                  {}
func (t *testUI) SetSettings(Settings)                                             {}
func (t *testUI) MessageDelivered(messageID, userID uuid.UUID)                     {}
func (t *testUI) SetDarkMode(value bool)                                           {}

func newBounceUI() UI {
	ui := newTestUI()
	network := newTestnet()

	go Start(
		network,
		ui,
	)

	return ui
}

func newBounce() *bounce {
	ui := newTestUI()
	network := newTestnet()

	b := &bounce{
		configDirectory: getConfigDirectory(),
		userInterface:   ui,
		network:         network,
		devicePool: &devicePool{
			devices:            make(map[string]*remoteDevice),
			groupPools:         make(map[uuid.UUID][]*remoteDevice),
			userPools:          make(map[uuid.UUID][]*remoteDevice),
			userOnlineStatus:   make(map[uuid.UUID]bool),
			deviceOnlineStatus: make(map[uuid.UUID]bool),
			lastDial:           make(map[string]time.Time),
			lastFailedDial:     make(map[string]time.Time),
			revokedDevices:     make(map[string]bool),
		},
	}
	b.openReferenceDatabase()

	b.userInterface.Build(
		b.configDirectory,
		UICallbacks{
			GetNewSyncString:                  b.getNewSyncString,
			RequestToSync:                     b.requestToSync,
			GetNewAddUserString:               b.getNewAddUserString,
			RequestToAddUser:                  b.requestToAddUser,
			SendDirectMessage:                 b.sendDirectMessage,
			CreateGroup:                       b.createGroup,
			SendGroupMessage:                  b.sendGroupMessage,
			AddUser:                           b.addUser,
			RemoveUser:                        b.removeUser,
			RenameGroup:                       b.renameGroup,
			SetGroupRetention:                 b.setGroupRetention,
			ClearGroupChatHistory:             b.clearGroupChatHistory,
			SetGroupMutedUntil:                b.setGroupMutedUntil,
			PromoteAdmin:                      b.promoteAdmin,
			DemoteAdmin:                       b.demoteAdmin,
			RestrictUserManagement:            b.restrictUserManagement,
			UnrestrictUserManagement:          b.unrestrictUserManagement,
			RestrictGroupEdits:                b.restrictGroupEdits,
			UnrestrictGroupEdits:              b.unrestrictGroupEdits,
			RestrictPosting:                   b.restrictPosting,
			UnrestrictPosting:                 b.unrestrictPosting,
			DeleteGroup:                       b.deleteGroup,
			BlockGroup:                        b.blockGroup,
			SetProfile:                        b.setProfile,
			ImportUser:                        b.importUser,
			ExportContact:                     b.exportContact,
			UserConnectionDesired:             b.userConnectionDesired,
			GroupConnectionDesired:            b.groupConnectionDesired,
			SetDMRetention:                    b.setDMRetention,
			SetDMMutedUntil:                   b.setDMMutedUntil,
			ClearDMChatHistory:                b.clearDMChatHistory,
			TypingInDirectMessage:             b.typingInDirectMessage,
			TypingInGroup:                     b.typingInGroup,
			UpdateProfileName:                 b.updateProfileName,
			RevokeDevice:                      b.revokeDevice,
			RenameDevice:                      b.renameDevice,
			MarkAsRead:                        b.markAsRead,
			NeverAskForBatteryOptimizations:   b.neverAskForBatteryOptimizations,
			SetReadReceiptsByDefault:          b.setReadReceiptsByDefault,
			SetTypingIndicatorsByDefault:      b.setTypingIndicatorsByDefault,
			SetNewGroupRetention:              b.setNewGroupRetention,
			SetNewGroupRestrictUserManagement: b.setNewGroupRestrictUserManagement,
			SetNewGroupRestrictGroupEdits:     b.setNewGroupRestrictGroupEdits,
			SetNewGroupRestrictPosting:        b.setNewGroupRestrictPosting,
			SetGroupReadReceiptSettings:       b.setGroupReadReceiptSettings,
			SetGroupTypingIndicatorSettings:   b.setGroupTypingIndicatorSettings,
			SetDMReadReceiptSettings:          b.setDMReadReceiptSettings,
			SetDMTypingIndicatorSettings:      b.setDMTypingIndicatorSettings,
			MarkAllGroupMessagesAsRead:        b.markAllGroupMessagesAsRead,
			MarkAllDirectMessagesAsRead:       b.markAllDirectMessagesAsRead,
			SetDarkMode:                       b.setDarkMode,
		},
	)

	b.network.Load(b.configDirectory)
	b.openDatabase()
	initialState := b.buildInitialState()
	b.userInterface.LoadInitialState(initialState)

	go b.network.Start(
		NetworkCallbacks{
			NetworkOnline:  b.networkOnline,
			NetworkOffline: b.networkOffline,
		},
	)

	go func() {
		b.userInterface.Run()
		b.shutdown()
	}()

	return b
}

func createUsersAndGroups(t *testing.T) (me, alice, bob *bounce, groupID uuid.UUID) {
	me = newBounce()
	me.setProfile("Me", "test")
	meExport := me.exportContact("export", time.Now().Unix()+100, false)

	alice = newBounce()
	alice.setProfile("Alice", "test")
	aliceExport := alice.exportContact("export", time.Now().Unix()+100, false)

	bob = newBounce()
	bob.setProfile("Bob", "test")
	bobExport := bob.exportContact("export", time.Now().Unix()+100, false)

	me.importUser(aliceExport)
	me.importUser(bobExport)
	alice.importUser(meExport)
	alice.importUser(bobExport)
	bob.importUser(meExport)
	bob.importUser(aliceExport)

	assert.NotEqual(t, uuid.Nil, me.currentUserID())
	assert.NotEqual(t, uuid.Nil, alice.currentUserID())
	assert.NotEqual(t, uuid.Nil, bob.currentUserID())

	me.createGroup(Group{
		Name: "Test Group",
		Users: []User{
			User{
				ID: me.currentUserID(),
			},
			User{
				ID: alice.currentUserID(),
			},
			User{
				ID: bob.currentUserID(),
			},
		},
		Admins: []uuid.UUID{me.currentUserID()},
	})
	await(alice, "NewGroupChat")
	await(bob, "NewGroupChat")

	var g group
	me.database.First(&g)
	groupID = g.ID

	t.Cleanup(func() {
		me.shutdown() // TODO: needed to avoid database errors with -cover but timing out connection closing and handler checks
		alice.shutdown()
		bob.shutdown()
		os.RemoveAll(me.configDirectory)
		os.RemoveAll(alice.configDirectory)
		os.RemoveAll(bob.configDirectory)
	})

	return
}
