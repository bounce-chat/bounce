package network

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bounce-chat/bounce/chat"
)

// shortLiveness shrinks every liveness duration so a test runs in well under a second
func shortLiveness(t *testing.T) {
	t.Helper()
	saved := []*time.Duration{&canaryDelay, &canaryKeepAliveInterval, &canaryReadWindow, &canaryRedialWindow, &canaryRedialPause, &canaryDialTimeout, &restartInterval, &restartBackoffLimit, &handshakeTimeout}
	previous := make([]time.Duration, len(saved))
	for i, d := range saved {
		previous[i] = *d
	}
	t.Cleanup(func() {
		for i, d := range saved {
			*d = previous[i]
		}
	})
	canaryDelay = 10 * time.Millisecond
	canaryKeepAliveInterval = 10 * time.Millisecond
	canaryReadWindow = 60 * time.Millisecond
	canaryRedialWindow = 150 * time.Millisecond
	canaryRedialPause = 20 * time.Millisecond
	canaryDialTimeout = 100 * time.Millisecond
	restartInterval = 10 * time.Millisecond
	restartBackoffLimit = 30 * time.Millisecond
	handshakeTimeout = 100 * time.Millisecond
}

// fakeSelf stands in for Tor carrying a connection from ourselves back to ourselves.  Each dial hands the canary the
// dialled end of one pipe and the accepted end of another, and relays bytes between the test's ends of the two, so
// the canary's keep-alives come back to it the way they would through a rendezvous.  Cutting the relay is the
// network dying under the canary.
type fakeSelf struct {
	accepted chan *torNetworkConnection
	fail     atomic.Bool
	dials    atomic.Int32

	mu    sync.Mutex
	relay []net.Conn
}

func newFakeSelf() *fakeSelf {
	return &fakeSelf{accepted: make(chan *torNetworkConnection, 1)}
}

func (f *fakeSelf) dial() (net.Conn, error) {
	f.dials.Add(1)
	if f.fail.Load() {
		return nil, errors.New("no circuit")
	}
	dialled, dialledPeer := net.Pipe()
	accepted, acceptedPeer := net.Pipe()
	f.mu.Lock()
	f.relay = append(f.relay, dialledPeer, acceptedPeer)
	f.mu.Unlock()
	go io.Copy(acceptedPeer, dialledPeer)
	f.accepted <- &torNetworkConnection{underlying: accepted, localAddress: &torAddress{address: "self"}, remoteAddress: &torAddress{address: "self"}}
	return dialled, nil
}

// cut severs every relay, killing the canary from the network's side
func (f *fakeSelf) cut() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.relay {
		c.Close()
	}
	f.relay = nil
}

type verdict struct {
	mu        sync.Mutex
	called    bool
	confirmed bool
}

func (v *verdict) dead(confirmed bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.called, v.confirmed = true, confirmed
}

func (v *verdict) get() (bool, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.called, v.confirmed
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

func newLivenessNetwork(t *testing.T) *TorNetwork {
	t.Helper()
	n, _ := newTestIdentity(t)
	n.callbacks = chat.NetworkCallbacks{NetworkOnline: func() {}, NetworkOffline: func() {}}
	return n
}

// startCanary runs the canary on a session that is stopped, and waited for, when the test ends: the duration vars
// it reads are restored by cleanup, so it must be gone before then
func startCanary(t *testing.T, n *TorNetwork, self *fakeSelf, v *verdict) {
	t.Helper()
	stopping := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		n.runCanaryWith(self.dial, self.accepted, stopping, v.dead)
	}()
	t.Cleanup(func() {
		close(stopping)
		self.cut()
		<-done
	})
}

// A canary whose bytes come back stays up, and nothing is declared dead
func TestCanaryStaysUpWhileItsBytesComeBack(t *testing.T) {
	shortLiveness(t)
	n := newLivenessNetwork(t)
	self := newFakeSelf()
	var v verdict
	startCanary(t, n, self, &v)

	time.Sleep(canaryDelay + 5*canaryReadWindow)
	if called, _ := v.get(); called {
		t.Fatal("a healthy canary was declared dead")
	}
	if got := self.dials.Load(); got != 1 {
		t.Errorf("a healthy canary was dialled %d times, want 1", got)
	}
}

// A canary that dies is redialled; when the redial keeps failing for the window, the session is given up on, and
// because a canary had connected on it the verdict says so
func TestCanaryRedialsWhenItDiesAndGivesUpWhenItCannot(t *testing.T) {
	shortLiveness(t)
	n := newLivenessNetwork(t)
	self := newFakeSelf()
	var v verdict
	startCanary(t, n, self, &v)
	if !waitFor(t, time.Second, func() bool { return self.dials.Load() == 1 }) {
		t.Fatal("the canary never dialled")
	}
	time.Sleep(2 * canaryReadWindow)

	// The network dies under it: the redial succeeds once, then the network is gone for good
	self.cut()
	if !waitFor(t, time.Second, func() bool { return self.dials.Load() >= 2 }) {
		t.Fatal("a dead canary was not redialled")
	}
	self.fail.Store(true)
	self.cut()

	if !waitFor(t, 2*canaryRedialWindow+time.Second, func() bool { called, _ := v.get(); return called }) {
		t.Fatal("the session was never given up on")
	}
	if _, confirmed := v.get(); !confirmed {
		t.Error("the verdict forgot that a canary had connected on this session")
	}
}

// A session whose canary never connects is given up on after the window, with a verdict that says it never did
func TestCanaryGivesUpOnASessionItNeverConnectedTo(t *testing.T) {
	shortLiveness(t)
	n := newLivenessNetwork(t)
	self := newFakeSelf()
	self.fail.Store(true)
	var v verdict
	startCanary(t, n, self, &v)

	if !waitFor(t, canaryDelay+2*canaryRedialWindow+time.Second, func() bool { called, _ := v.get(); return called }) {
		t.Fatal("the session was never given up on")
	}
	if _, confirmed := v.get(); confirmed {
		t.Error("the verdict claims a canary connected when none did")
	}
	if got := self.dials.Load(); got < 2 {
		t.Errorf("only %d dial attempts inside the window", got)
	}
}

// Peers that are still talking to us veto the verdict: the canary keeps trying but nothing is declared dead until
// they fall silent too
func TestPeersThatAreTalkingVetoTheVerdict(t *testing.T) {
	shortLiveness(t)
	n := newLivenessNetwork(t)
	self := newFakeSelf()
	self.fail.Store(true)
	var v verdict
	startCanary(t, n, self, &v)

	// A peer connection reading bytes, as the handshake wrappers record it
	talking := make(chan struct{})
	quiet := make(chan struct{})
	pace := canaryReadWindow / 4
	go func() {
		defer close(quiet)
		for {
			select {
			case <-talking:
				return
			default:
				n.lastPeerRead.Store(time.Now().UnixNano())
				time.Sleep(pace)
			}
		}
	}()
	t.Cleanup(func() { <-quiet })

	time.Sleep(canaryDelay + 3*canaryRedialWindow)
	if called, _ := v.get(); called {
		t.Fatal("the network was declared dead while peers were still talking")
	}

	close(talking)
	if !waitFor(t, 2*canaryRedialWindow+canaryReadWindow+time.Second, func() bool { called, _ := v.get(); return called }) {
		t.Fatal("the network was never declared dead after the peers fell silent")
	}
}

// Recovery restarts at once for a session that was healthy, retries a failing bootstrap at the fixed interval with
// no backoff, and backs off only before restarting a session that bootstrapped without its canary ever connecting
func TestRecoveryBacksOffOnlyForBootstrapsThatWentNowhere(t *testing.T) {
	shortLiveness(t)
	n := newLivenessNetwork(t)
	var sleeps []time.Duration
	sleep := func(d time.Duration) bool { sleeps = append(sleeps, d); return true }
	script := []error{}
	restart := func() error {
		if len(script) == 0 {
			return nil
		}
		err := script[0]
		script = script[1:]
		return err
	}
	run := func(confirmed bool, errs ...error) []time.Duration {
		sleeps = nil
		script = errs
		n.recovering.Store(true)
		n.recoverNetwork(confirmed, restart, sleep)
		return sleeps
	}
	eq := func(got []time.Duration, want ...time.Duration) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("sleeps %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("sleeps %v, want %v", got, want)
			}
		}
	}
	base, limit := restartInterval, restartBackoffLimit
	down := errors.New("bootstrap failed")

	// A healthy session died: restart now, then every interval while bootstrap fails, no backoff
	eq(run(true, down, down), base, base)
	if n.restartBackoff.Load() != 0 {
		t.Error("failed bootstraps grew the backoff")
	}

	// Bootstraps that come up with an unreachable service back off: base, doubled, capped
	eq(run(false), base)
	eq(run(false), 2*base)
	eq(run(false), limit)
	eq(run(false), limit)

	// A failed bootstrap in the middle resets it, so the internet returning is not made to wait
	eq(run(false, down), limit, base)
	eq(run(false), base)

	// So does a canary that connects, which runCanaryWith records by zeroing the backoff
	n.restartBackoff.Store(int64(limit))
	n.restartBackoff.Store(0)
	eq(run(false), base)

	// Shutdown ends the loop
	eq(run(true, errNetworkStopped))
	if n.recovering.Load() {
		t.Error("recovery did not release its flag")
	}
}

// Recovery reports offline before it restarts, and only once
func TestRecoveryReportsOfflineOnce(t *testing.T) {
	shortLiveness(t)
	n := newLivenessNetwork(t)
	var offline atomic.Int32
	n.callbacks = chat.NetworkCallbacks{NetworkOnline: func() {}, NetworkOffline: func() { offline.Add(1) }}
	n.online = true
	n.recovering.Store(true)
	n.recoverNetwork(true, func() error { return nil }, func(time.Duration) bool { return true })
	if offline.Load() != 1 {
		t.Errorf("offline reported %d times, want 1", offline.Load())
	}
}

// The accept pump hands a connection from our own address to the canary, never to the engine
func TestAcceptPumpRoutesOurOwnAddressToTheCanary(t *testing.T) {
	shortHandshakeTimeout(t, 3*time.Second)
	listener, address := newTestIdentity(t)
	fake := newFakeListener()
	accepted := make(chan *torNetworkConnection, acceptQueueDepth)
	canary := make(chan *torNetworkConnection, 1)
	stopping := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		listener.runAcceptPump(fake, address, accepted, canary, stopping)
	}()

	listenerSide, dialerSide := net.Pipe()
	fake.conns <- listenerSide
	go func() { _, _ = listener.handshakeDialer(address, address, dialerSide) }()

	select {
	case conn := <-canary:
		if conn.tracker != nil {
			t.Error("the canary's bytes would count as peer traffic")
		}
	case conn := <-accepted:
		t.Fatalf("our own connection was handed to the engine as peer %q", conn.RemoteAddr())
	case <-time.After(2 * time.Second):
		t.Fatal("our own connection went nowhere")
	}

	fake.stop()
	done.Wait()
}

// The engine's connections record when they last read, which is what vouches for the network
func TestPeerConnectionsRecordTheirReads(t *testing.T) {
	listener, listenerAddress := newTestIdentity(t)
	dialer, dialerAddress := newTestIdentity(t)
	listenerConn, err, dialerConn, dialErr := runHandshakePair(t, listener, listenerAddress, dialer, dialerAddress)
	if err != nil || dialErr != nil {
		t.Fatalf("handshake: %v / %v", err, dialErr)
	}
	defer listenerConn.Close()
	defer dialerConn.Close()
	if listener.peersRecentlyRead() {
		t.Fatal("a read was recorded before any happened")
	}
	go dialerConn.Write([]byte("hi"))
	buf := make([]byte, 2)
	if _, err := listenerConn.Read(buf); err != nil {
		t.Fatal(err)
	}
	if !listener.peersRecentlyRead() {
		t.Error("a peer read was not recorded")
	}
}

// A dial that completes but whose accepting end never arrives through our own listener is a failed attempt: the
// dialled end is closed and the attempt is retried until the window runs out
func TestCanaryGivesUpWhenOurListenerNeverAccepts(t *testing.T) {
	shortLiveness(t)
	n := newLivenessNetwork(t)
	self := newFakeSelf()
	var dialled []*countingConn
	var mu sync.Mutex
	dialOnly := func() (net.Conn, error) {
		self.dials.Add(1)
		a, b := net.Pipe()
		t.Cleanup(func() { b.Close() })
		c := &countingConn{Conn: a}
		mu.Lock()
		dialled = append(dialled, c)
		mu.Unlock()
		return c, nil
	}
	// A stale accepting end from an earlier attempt is already waiting, and must be discarded, not paired
	stale := &countingConn{}
	stale.Conn, _ = net.Pipe()
	self.accepted <- &torNetworkConnection{underlying: stale}

	stopping := make(chan struct{})
	done := make(chan struct{})
	var v verdict
	go func() {
		defer close(done)
		n.runCanaryWith(dialOnly, self.accepted, stopping, v.dead)
	}()
	t.Cleanup(func() { close(stopping); <-done })

	if !waitFor(t, canaryDelay+3*canaryRedialWindow+time.Second, func() bool { called, _ := v.get(); return called }) {
		t.Fatal("a canary our listener never accepted was not given up on")
	}
	if _, confirmed := v.get(); confirmed {
		t.Error("an unaccepted canary counted as connected")
	}
	if !stale.closed.Load() {
		t.Error("the stale accepting end was not closed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(dialled) < 2 {
		t.Errorf("only %d attempts inside the window", len(dialled))
	}
	for i, c := range dialled {
		if !c.closed.Load() {
			t.Errorf("dialled end %d was left open", i)
		}
	}
}

// Shutdown ends the canary at once, wherever it is, and nothing is declared dead on the way out
func TestShutdownStopsTheCanary(t *testing.T) {
	shortLiveness(t)
	canaryDelay = time.Hour // parked in its initial wait
	n := newLivenessNetwork(t)
	self := newFakeSelf()
	stopping := make(chan struct{})
	done := make(chan struct{})
	var v verdict
	go func() {
		defer close(done)
		n.runCanaryWith(self.dial, self.accepted, stopping, v.dead)
	}()

	n.Shutdown()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("the canary outlived shutdown")
	}
	if called, _ := v.get(); called {
		t.Error("shutdown was reported as a dead network")
	}
}

// A verdict on a session that has already been replaced, or while a recovery is running, or during shutdown, starts
// nothing: those are not new information
func TestVerdictsThatAreNotNewsStartNoRecovery(t *testing.T) {
	n := newLivenessNetwork(t)
	current := &session{}
	n.session = current

	n.networkDead(&session{}, true) // replaced
	if n.recovering.Load() {
		t.Fatal("a verdict on a replaced session started a recovery")
	}

	n.recovering.Store(true) // one already running
	n.networkDead(current, true)
	n.recovering.Store(false)

	n.shutdown = true
	n.networkDead(current, true)
	if n.recovering.Load() {
		t.Fatal("a verdict during shutdown started a recovery")
	}
}
