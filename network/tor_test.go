package network

import (
	"crypto/rand"
	"crypto/sha512"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	arti "github.com/bounce-chat/go-arti"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// newTestIdentity mints a usable onion identity without a Tor client running.
//
// arti.Sign wants an EXPANDED ed25519 key (64 bytes), which crypto/ed25519
// cannot produce: its keys are seed-derived. The expansion is the standard one
// - SHA512 of a seed, with the usual clamping - and arti.PublicKeyFromPrivate
// derives the public half from it, so the pair round-trips through the same
// code path the real hidden-service key uses.
func newTestIdentity(t *testing.T) (*TorNetwork, string) {
	t.Helper()

	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	h := sha512.Sum512(seed)
	h[0] &= 248
	h[31] &= 127
	h[31] |= 64
	expanded := make([]byte, arti.PrivateKeySize)
	copy(expanded, h[:arti.PrivateKeySize])

	pub, err := arti.PublicKeyFromPrivate(expanded)
	if err != nil {
		t.Fatalf("deriving public key: %v", err)
	}
	address, err := arti.OnionIDFromPublicKey(pub)
	if err != nil {
		t.Fatalf("deriving onion id: %v", err)
	}

	network := &TorNetwork{privateKey: expanded, publicKey: pub}

	// Sanity: the identity must actually verify under the real code path, or
	// every test below would be asserting against a broken fixture.
	message := []byte("fixture self-check")
	if !network.VerifySignature(address, message, network.Sign(message)) {
		t.Fatal("fixture identity does not verify against VerifySignature")
	}
	return network, address
}

// shortHandshakeTimeout shortens the handshake budget for the duration of a
// test and restores it afterwards.
func shortHandshakeTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	previous := handshakeTimeout
	handshakeTimeout = d
	t.Cleanup(func() { handshakeTimeout = previous })
}

// fakeListener feeds the pump a scripted sequence of connections and then an
// error, standing in for *arti.OnionService.
type fakeListener struct {
	conns chan net.Conn
	// closed makes Accept return an error, which is how a real listener
	// reports that it has gone away.
	closed chan struct{}
}

func newFakeListener() *fakeListener {
	return &fakeListener{
		conns:  make(chan net.Conn, 512),
		closed: make(chan struct{}),
	}
}

func (f *fakeListener) Accept() (net.Conn, error) {
	select {
	case c := <-f.conns:
		return c, nil
	case <-f.closed:
		return nil, errors.New("listener closed")
	}
}

func (f *fakeListener) stop() { close(f.closed) }

// countingConn reports whether it has been closed, so tests can assert that a
// rejected peer's socket is not leaked.
type countingConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *countingConn) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

// runHandshakePair drives both halves against each other over a net.Pipe and
// returns the listener-side result.
func runHandshakePair(
	t *testing.T,
	listener *TorNetwork, listenerAddress string,
	dialer *TorNetwork, dialerAddress string,
) (*torNetworkConnection, error, *torNetworkConnection, error) {
	t.Helper()

	listenerSide, dialerSide := net.Pipe()

	type result struct {
		conn *torNetworkConnection
		err  error
	}
	dialerDone := make(chan result, 1)
	go func() {
		c, err := dialer.handshakeDialer(dialerAddress, listenerAddress, dialerSide)
		dialerDone <- result{c, err}
	}()

	listenerConn, listenerErr := listener.handshakeListener(listenerAddress, listenerSide)

	select {
	case r := <-dialerDone:
		return listenerConn, listenerErr, r.conn, r.err
	case <-time.After(5 * time.Second):
		t.Fatal("dialer half did not finish")
		return nil, nil, nil, nil
	}
}

// ---------------------------------------------------------------------------
// handshake
// ---------------------------------------------------------------------------

func TestHandshakeHappyPath(t *testing.T) {
	listener, listenerAddress := newTestIdentity(t)
	dialer, dialerAddress := newTestIdentity(t)

	lConn, lErr, dConn, dErr := runHandshakePair(t, listener, listenerAddress, dialer, dialerAddress)
	if lErr != nil {
		t.Fatalf("listener half failed: %v", lErr)
	}
	if dErr != nil {
		t.Fatalf("dialer half failed: %v", dErr)
	}

	// Each side must end up believing it is talking to the other, because the
	// engine trusts RemoteAddr() as the peer's authenticated identity.
	if got := lConn.RemoteAddr().String(); got != dialerAddress {
		t.Errorf("listener sees peer %q, want the dialer %q", got, dialerAddress)
	}
	if got := lConn.LocalAddr().String(); got != listenerAddress {
		t.Errorf("listener reports itself as %q, want %q", got, listenerAddress)
	}
	if got := dConn.RemoteAddr().String(); got != listenerAddress {
		t.Errorf("dialer sees peer %q, want the listener %q", got, listenerAddress)
	}
	if got := dConn.LocalAddr().String(); got != dialerAddress {
		t.Errorf("dialer reports itself as %q, want %q", got, dialerAddress)
	}
}

func TestHandshakeClearsDeadlineOnSuccess(t *testing.T) {
	// A deadline left on the socket would later kill a perfectly healthy peer,
	// so success must hand the engine a socket with no deadline set.
	shortHandshakeTimeout(t, 2*time.Second)

	listener, listenerAddress := newTestIdentity(t)
	dialer, dialerAddress := newTestIdentity(t)

	lConn, lErr, dConn, dErr := runHandshakePair(t, listener, listenerAddress, dialer, dialerAddress)
	if lErr != nil || dErr != nil {
		t.Fatalf("handshake failed: listener=%v dialer=%v", lErr, dErr)
	}

	// Past the old budget, the connection must still carry data.
	time.Sleep(2500 * time.Millisecond)

	done := make(chan error, 1)
	go func() {
		_, err := dConn.Write([]byte("still open"))
		done <- err
	}()
	buf := make([]byte, len("still open"))
	if _, err := lConn.Read(buf); err != nil {
		t.Fatalf("read after the handshake budget expired: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("write after the handshake budget expired: %v", err)
	}
}

func TestHandshakeRejectsSilentPeerAndClosesSocket(t *testing.T) {
	// The whole point of item 6: a peer that connects and says nothing must be
	// dropped on a timer rather than parking the goroutine forever.
	shortHandshakeTimeout(t, 250*time.Millisecond)

	listener, listenerAddress := newTestIdentity(t)
	listenerSide, dialerSide := net.Pipe()
	defer dialerSide.Close()
	tracked := &countingConn{Conn: listenerSide}

	done := make(chan error, 1)
	go func() {
		_, err := listener.handshakeListener(listenerAddress, tracked)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a silent peer completed the handshake")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handshake did not give up on a silent peer")
	}
	if !tracked.closed.Load() {
		t.Error("socket of a rejected peer was not closed, leaking a descriptor")
	}
}

func TestHandshakeRejectsNonCanonicalAddress(t *testing.T) {
	// An uppercase spelling verifies under PublicKeyFromOnionID but keys
	// differently everywhere downstream, so it must be refused outright.
	listener, listenerAddress := newTestIdentity(t)
	_, dialerAddress := newTestIdentity(t)

	upper := ""
	for _, r := range dialerAddress {
		if r >= 'a' && r <= 'z' {
			upper += string(r - 32)
		} else {
			upper += string(r)
		}
	}

	listenerSide, dialerSide := net.Pipe()
	tracked := &countingConn{Conn: listenerSide}
	go func() {
		defer dialerSide.Close()
		write(dialerSide, []byte(upper))
		nonce := make([]byte, handshakeNonceSize)
		write(dialerSide, nonce)
	}()

	_, err := listener.handshakeListener(listenerAddress, tracked)
	if err == nil {
		t.Fatal("a non-canonical dialer address was accepted")
	}
	if !tracked.closed.Load() {
		t.Error("socket was not closed after rejecting a non-canonical address")
	}
}

func TestHandshakeRejectsWrongDialerSignature(t *testing.T) {
	// A dialer that cannot sign for the address it claims must be refused. This
	// is the property the engine's whole authorization model rests on.
	listener, listenerAddress := newTestIdentity(t)
	_, victimAddress := newTestIdentity(t)
	impostor, _ := newTestIdentity(t)

	listenerSide, dialerSide := net.Pipe()
	tracked := &countingConn{Conn: listenerSide}

	go func() {
		defer dialerSide.Close()
		// Claim the victim's address but sign with the impostor's key.
		_, _ = impostor.handshakeDialer(victimAddress, listenerAddress, dialerSide)
	}()

	_, err := listener.handshakeListener(listenerAddress, tracked)
	if err == nil {
		t.Fatal("an impostor authenticated as another address")
	}
	if !tracked.closed.Load() {
		t.Error("socket was not closed after rejecting a bad signature")
	}
}

func TestHandshakeDialerRejectsWrongListener(t *testing.T) {
	// The dialer must refuse a listener that cannot sign for the address it
	// dialled - this is what stops a relay from terminating the connection.
	_, intendedAddress := newTestIdentity(t)
	wrongListener, wrongAddress := newTestIdentity(t)
	dialer, dialerAddress := newTestIdentity(t)

	listenerSide, dialerSide := net.Pipe()
	go func() {
		defer listenerSide.Close()
		// The wrong listener runs a perfectly correct handshake for ITSELF.
		_, _ = wrongListener.handshakeListener(wrongAddress, listenerSide)
	}()

	// But the dialer believes it dialled intendedAddress.
	_, err := dialer.handshakeDialer(dialerAddress, intendedAddress, dialerSide)
	if err == nil {
		t.Fatal("dialer accepted a listener that could not sign for the dialled address")
	}
}

// ---------------------------------------------------------------------------
// accept pump
// ---------------------------------------------------------------------------

// startPump runs the pump against a fake listener and returns its queue.
func startPump(t *testing.T, listener *TorNetwork, listenerAddress string, fake *fakeListener) (chan *torNetworkConnection, chan struct{}, *sync.WaitGroup) {
	t.Helper()
	accepted := make(chan *torNetworkConnection, acceptQueueDepth)
	stopping := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		listener.runAcceptPump(fake, listenerAddress, accepted, stopping)
	}()
	return accepted, stopping, &done
}

func TestAcceptPumpDoesNotLetOneSilentPeerBlockOthers(t *testing.T) {
	// This is the regression test for item 7. Before the pump, the handshake ran
	// inline on the accept loop, so the silent peer below would have stalled
	// every connection behind it - permanently.
	shortHandshakeTimeout(t, 3*time.Second)

	listener, listenerAddress := newTestIdentity(t)
	fake := newFakeListener()
	accepted, _, done := startPump(t, listener, listenerAddress, fake)

	// A peer that connects and never speaks, queued FIRST.
	silentListenerSide, silentDialerSide := net.Pipe()
	defer silentDialerSide.Close()
	fake.conns <- silentListenerSide

	// Then a peer that completes normally.
	dialer, dialerAddress := newTestIdentity(t)
	goodListenerSide, goodDialerSide := net.Pipe()
	fake.conns <- goodListenerSide
	go func() {
		_, _ = dialer.handshakeDialer(dialerAddress, listenerAddress, goodDialerSide)
	}()

	select {
	case conn := <-accepted:
		if conn == nil {
			t.Fatal("queue closed instead of delivering a connection")
		}
		if got := conn.RemoteAddr().String(); got != dialerAddress {
			t.Errorf("delivered peer %q, want %q", got, dialerAddress)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a silent peer blocked a healthy one: the handshake is still serialised")
	}

	fake.stop()
	done.Wait()
}

func TestAcceptPumpClosesQueueWhenListenerStops(t *testing.T) {
	// Accept distinguishes shutdown from restart by seeing the queue close, so
	// the pump must always close it on the way out.
	listener, listenerAddress := newTestIdentity(t)
	fake := newFakeListener()
	accepted, stopping, done := startPump(t, listener, listenerAddress, fake)

	fake.stop()
	done.Wait()

	select {
	case _, ok := <-accepted:
		if ok {
			t.Fatal("queue delivered a connection after the listener stopped")
		}
	default:
		t.Fatal("queue was not closed when the listener stopped")
	}
	select {
	case <-stopping:
	default:
		t.Fatal("stopping was not closed when the listener stopped")
	}
}

func TestAcceptPumpBoundsConcurrentHandshakes(t *testing.T) {
	// An inbound flood must not create unbounded goroutines. Every connection
	// here is silent, so each occupies a slot for the whole budget.
	shortHandshakeTimeout(t, 2*time.Second)

	listener, listenerAddress := newTestIdentity(t)
	fake := newFakeListener()

	var inFlight atomic.Int64
	var peak atomic.Int64
	flood := maxConcurrentHandshakes + 64
	sides := make([]net.Conn, 0, flood)
	for i := 0; i < flood; i++ {
		listenerSide, dialerSide := net.Pipe()
		sides = append(sides, dialerSide)
		// Wrap so we can count how many handshakes are live at once.
		fake.conns <- &countingSlotConn{Conn: listenerSide, inFlight: &inFlight, peak: &peak}
	}
	defer func() {
		for _, c := range sides {
			c.Close()
		}
	}()

	accepted, _, done := startPump(t, listener, listenerAddress, fake)

	// Give the pump time to saturate, then check the ceiling held.
	time.Sleep(500 * time.Millisecond)
	if got := peak.Load(); got > int64(maxConcurrentHandshakes) {
		t.Errorf("peak concurrent handshakes %d exceeds the %d cap", got, maxConcurrentHandshakes)
	}
	if peak.Load() == 0 {
		t.Error("no handshakes ran at all, the test is not exercising the pump")
	}

	fake.stop()
	done.Wait()
	if len(accepted) != 0 {
		t.Errorf("silent peers produced %d accepted connections", len(accepted))
	}
}

// countingSlotConn records how many handshakes are reading concurrently.
type countingSlotConn struct {
	net.Conn
	inFlight *atomic.Int64
	peak     *atomic.Int64
	counted  atomic.Bool
}

func (c *countingSlotConn) Read(b []byte) (int, error) {
	if c.counted.CompareAndSwap(false, true) {
		live := c.inFlight.Add(1)
		for {
			previous := c.peak.Load()
			if live <= previous || c.peak.CompareAndSwap(previous, live) {
				break
			}
		}
	}
	return c.Conn.Read(b)
}

func (c *countingSlotConn) Close() error {
	if c.counted.Load() {
		c.inFlight.Add(-1)
		c.counted.Store(false)
	}
	return c.Conn.Close()
}

func TestAcceptPumpClosesConnectionsNobodyCollects(t *testing.T) {
	// If the pump stops while a completed connection is waiting to be handed
	// over, that socket must be closed rather than leaked.
	listener, listenerAddress := newTestIdentity(t)
	dialer, dialerAddress := newTestIdentity(t)
	fake := newFakeListener()

	// A queue with no capacity, so the worker parks trying to publish.
	accepted := make(chan *torNetworkConnection)
	stopping := make(chan struct{})
	var done sync.WaitGroup
	done.Add(1)
	go func() {
		defer done.Done()
		listener.runAcceptPump(fake, listenerAddress, accepted, stopping)
	}()

	listenerSide, dialerSide := net.Pipe()
	tracked := &countingConn{Conn: listenerSide}
	fake.conns <- tracked
	dialerFinished := make(chan struct{})
	go func() {
		defer close(dialerFinished)
		_, _ = dialer.handshakeDialer(dialerAddress, listenerAddress, dialerSide)
	}()
	<-dialerFinished

	// Never read from accepted. Stop the pump and require the orphan be closed.
	fake.stop()
	done.Wait()

	if !tracked.closed.Load() {
		t.Error("a completed connection nobody collected was leaked instead of closed")
	}
}

// ---------------------------------------------------------------------------
// Accept
// ---------------------------------------------------------------------------

func TestAcceptDeliversCompletedConnection(t *testing.T) {
	listener, listenerAddress := newTestIdentity(t)
	s := &session{
		accepted: make(chan *torNetworkConnection, acceptQueueDepth),
		stopping: make(chan struct{}),
	}
	listener.session = s
	listener.ready = make(chan struct{})
	close(listener.ready)

	want := &torNetworkConnection{
		localAddress:  &torAddress{address: listenerAddress},
		remoteAddress: &torAddress{address: "peer"},
	}
	s.accepted <- want

	got, err, fatal := listener.Accept()
	if err != nil || fatal {
		t.Fatalf("Accept returned err=%v fatal=%v", err, fatal)
	}
	if got != net.Conn(want) {
		t.Error("Accept delivered a different connection than the pump queued")
	}
}

func TestAcceptReportsRestartWhenSessionSuperseded(t *testing.T) {
	// A restart must look non-fatal, or the accept loop tears the process down.
	listener, _ := newTestIdentity(t)
	old := &session{
		accepted: make(chan *torNetworkConnection),
		stopping: make(chan struct{}),
	}
	listener.session = old
	listener.ready = make(chan struct{})
	close(listener.ready)

	// Supersede it, then close the old queue the way a stopped pump would.
	listener.session = &session{
		accepted: make(chan *torNetworkConnection),
		stopping: make(chan struct{}),
	}
	close(old.accepted)

	// Accept against the superseded session directly.
	_, err, fatal := listener.acceptFrom(old)
	if !errors.Is(err, errNetworkRestarting) {
		t.Errorf("err = %v, want errNetworkRestarting", err)
	}
	if fatal {
		t.Error("a restart was reported as fatal, which stops the accept loop")
	}
}

func TestAcceptReportsStoppedOnShutdown(t *testing.T) {
	listener, _ := newTestIdentity(t)
	s := &session{
		accepted: make(chan *torNetworkConnection),
		stopping: make(chan struct{}),
	}
	listener.session = s
	listener.ready = make(chan struct{})
	close(listener.ready)

	listener.shutdown = true
	close(s.accepted)

	_, err, fatal := listener.acceptFrom(s)
	if !errors.Is(err, errNetworkStopped) {
		t.Errorf("err = %v, want errNetworkStopped", err)
	}
	if !fatal {
		t.Error("shutdown must be fatal so the accept loop exits")
	}
}
