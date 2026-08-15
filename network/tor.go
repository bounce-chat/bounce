package network

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bounce-chat/bounce/chat"
	arti "github.com/bounce-chat/go-arti"
	log "github.com/sirupsen/logrus"
)

// torLock guards the online flag, which the status watcher updates and the
// callbacks read.
var torLock sync.Mutex

var handshakeChallengeSize = 32
var signatureSize = 64

// restartWaitLimit bounds how long Accept and Dial will wait for a restart to
// finish. It exists only so a restart that never completes cannot wedge the
// accept loop forever; callers get a non-fatal error and come straight back.
const restartWaitLimit = 4 * time.Minute

// bootstrapTimeout bounds how long a restart waits for Tor to come back.
//
// Without it the wait is unbounded: Bootstrap returns only when the client is
// ready, the client is closed, or the context is done, so with no internet it
// never returns at all - holding restartMutex, leaving the session nil, and
// removing the caller's chance to try again later. A warm bootstrap takes
// about a second and a cold one a few, so anything past a minute already means
// the network is down; three gives generous headroom while still fitting
// inside the caller's retry interval.
const bootstrapTimeout = 3 * time.Minute

// errNetworkRestarting is returned, non-fatally, while the Tor client is being
// replaced. Callers should retry rather than treat it as a failure.
var errNetworkRestarting = errors.New("network is restarting")

// errNetworkStopped is returned once Shutdown has begun.
var errNetworkStopped = errors.New("network is stopped")

// session is one generation of the Tor client and the listener published on it.
//
// It is never mutated after construction. Restarting builds a whole new session
// and swaps it in, so a caller either sees a matched client/onion pair or none
// at all - never a client from one generation beside an onion from the next.
// Everything that touches Tor goes through awaitSession to get one.
type session struct {
	client *arti.Client
	onion  *arti.OnionService
}

type TorNetwork struct {
	routerDirectory string
	keyDirectory    string
	callbacks       chat.NetworkCallbacks
	publicKey       ed25519.PublicKey
	privateKey      []byte
	online          bool
	shutdown        bool
	shutdownMutex   sync.Mutex

	// sessionMutex guards session and ready. session is nil whenever there is
	// no usable client, which is the case during a restart.
	//
	// ready is the readiness signal, and it works the way closed channels do:
	// while it is OPEN, receiving from it blocks, so waiters park. Closing it
	// releases every waiter at once, permanently. So a restart installs a fresh
	// open channel to make callers wait, and closing it is what says "go".
	// Signalling this way rather than polling a flag is what keeps the accept
	// loop from spinning while the network is down.
	sessionMutex sync.RWMutex
	session      *session
	ready        chan struct{}

	// restartMutex serialises restarts.
	restartMutex sync.Mutex

	// stopped is closed by Shutdown, releasing Start.
	stopped     chan struct{}
	stoppedOnce sync.Once
}

// readyChannel returns the current readiness gate, creating it on first use.
func (bounceTor *TorNetwork) readyChannel() chan struct{} {
	bounceTor.sessionMutex.Lock()
	defer bounceTor.sessionMutex.Unlock()
	if bounceTor.ready == nil {
		bounceTor.ready = make(chan struct{})
	}
	return bounceTor.ready
}

// awaitSession blocks until a usable session exists, and is the only way to get
// at the client or the onion service.
//
// It blocks rather than failing fast on purpose: the accept loop retries a
// non-fatal error immediately with no backoff, so returning straight away
// during a restart would spin a core until the restart finished.
func (bounceTor *TorNetwork) awaitSession() (*session, error) {
	for {
		bounceTor.sessionMutex.RLock()
		current, ready := bounceTor.session, bounceTor.ready
		bounceTor.sessionMutex.RUnlock()

		if current != nil {
			return current, nil
		}
		if ready == nil {
			ready = bounceTor.readyChannel()
		}

		select {
		case <-ready:
			// A restart finished. Re-read: it may have succeeded, in which case
			// the next pass returns the new session.
		case <-bounceTor.stopChannel():
			return nil, errNetworkStopped
		case <-time.After(restartWaitLimit):
			return nil, errNetworkRestarting
		}
	}
}

// peekSession returns the current session without waiting, for callers that
// have something useful to do when Tor is unavailable.
func (bounceTor *TorNetwork) peekSession() *session {
	bounceTor.sessionMutex.RLock()
	defer bounceTor.sessionMutex.RUnlock()
	return bounceTor.session
}

// superseded reports whether s has already been replaced, which is how Accept
// and Dial tell "the network is restarting under me" from a real failure.
func (bounceTor *TorNetwork) superseded(s *session) bool {
	bounceTor.sessionMutex.RLock()
	defer bounceTor.sessionMutex.RUnlock()
	return bounceTor.session != s
}

// shuttingDown reports whether Shutdown has been entered.
func (bounceTor *TorNetwork) shuttingDown() bool {
	bounceTor.shutdownMutex.Lock()
	defer bounceTor.shutdownMutex.Unlock()
	return bounceTor.shutdown
}

// stopChannel returns the channel closed on shutdown, creating it on first use
// so a zero-value TorNetwork still works.
func (bounceTor *TorNetwork) stopChannel() chan struct{} {
	bounceTor.shutdownMutex.Lock()
	defer bounceTor.shutdownMutex.Unlock()
	if bounceTor.stopped == nil {
		bounceTor.stopped = make(chan struct{})
	}
	return bounceTor.stopped
}

func (bounceTor *TorNetwork) loadConfig(configDirectory string) {
	bounceTor.routerDirectory = configDirectory + "/tor/router"
	bounceTor.keyDirectory = configDirectory + "/tor/keys"

	// Create the router directory if needed
	err := os.MkdirAll(bounceTor.routerDirectory, 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating tor router directory")
	}

	// Create the key directory if needed
	err = os.MkdirAll(bounceTor.keyDirectory, 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error creating tor key directory")
	}

	// Load or create the keypair for the hidden service
	pubkey, privkey := bounceTor.hiddenServiceKey()
	bounceTor.publicKey = pubkey // TODO: needed?  move to the get function?
	bounceTor.privateKey = privkey
}

func (bounceTor *TorNetwork) hiddenServiceKey() (ed25519.PublicKey, []byte) { // TODO: reverse the order?
	// Create the config directory if needed
	err := os.MkdirAll(bounceTor.keyDirectory, 0700)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
			"path":  bounceTor.keyDirectory,
		}).Fatal("error creating hidden service key directory")
	}

	// Check for keys on disk, return them if they exist
	privateKeyFile := bounceTor.keyDirectory + "/private_key"
	publicKeyFile := bounceTor.keyDirectory + "/public_key"
	privateKeyBytes, err := os.ReadFile(privateKeyFile)
	if err != nil {
		log.WithFields(log.Fields{
			"path": privateKeyFile,
		}).Debug("no hidden service private key found, generating new key pair")
	} else {
		publicKeyBytes, err := os.ReadFile(publicKeyFile)
		if err != nil {
			// We have the private key but the public key is missing.  This is weird, but we can regenerate it.
			pubkey, err := arti.PublicKeyFromPrivate(privateKeyBytes)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("stored private key is not usable")
			}
			err = os.WriteFile(publicKeyFile, pubkey, 0600)
			if err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Fatal("error writing regenerated public key")
			}
			return pubkey, privateKeyBytes
		} else {
			return publicKeyBytes, privateKeyBytes
		}
	}

	// The keys do not exist.  Tor generates them when the service is first
	// published, so there is nothing to write yet; saveHiddenServiceKey stores
	// whatever the service comes back with.
	return nil, nil
}

// saveHiddenServiceKey persists the identity of a freshly published service.
func (bounceTor *TorNetwork) saveHiddenServiceKey(privateKey []byte, publicKey ed25519.PublicKey) {
	if err := os.WriteFile(bounceTor.keyDirectory+"/public_key", publicKey, 0600); err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error writing public key")
	}
	if err := os.WriteFile(bounceTor.keyDirectory+"/private_key", privateKey, 0600); err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error writing private key")
	}
	bounceTor.publicKey = publicKey
	bounceTor.privateKey = privateKey
}

func (bounceTor *TorNetwork) Load(configDirectory string) {
	bounceTor.loadConfig(configDirectory)
}

func (bounceTor *TorNetwork) Start(callbacks chat.NetworkCallbacks) {
	bounceTor.callbacks = callbacks
	log.Info("connecting to the Tor network")

	// Route Arti's own logs here, so bootstrap and publication are visible in
	// the application log rather than only on stderr.
	bounceTor.forwardTorLogs()

	s, err := bounceTor.openSession()
	if err != nil {
		bounceTor.startupFailed(err, "failed to start TOR")
		return
	}
	bounceTor.publishSession(s)

	// Only now report connectivity. NetworkOnline is what starts the accept
	// loop, so announcing it any earlier hands out a service that does not yet
	// exist to accept on.
	go bounceTor.watchOnlineStatus(s)

	// Start blocks for the lifetime of the network, as it did before, but
	// returns on shutdown rather than leaving the goroutine parked forever.
	<-bounceTor.stopChannel()
}

// openSession brings up a Tor client and publishes the hidden service on it.
//
// Shared by Start and Restart, which is why it reports errors rather than
// calling startupFailed: a failure at startup is fatal, but the same failure
// during a restart just means we try again later.
func (bounceTor *TorNetwork) openSession() (*session, error) {
	// In rare situations, arti has been known to put itself in a state where the information
	// on disk prevents a successful bootstrap.  This seems related to devices that transition
	// from one network to another often, like mobile phones.  The solution, for now, is to
	// simply clear the state on disk at every launch, since the speed of bootstrapping is not
	// negatively affected too much.
	os.RemoveAll(bounceTor.routerDirectory)
	os.MkdirAll(bounceTor.routerDirectory, 0700)

	client, err := arti.Open(arti.Config{DataDir: bounceTor.routerDirectory})
	if err != nil {
		return nil, err
	}

	// Bound the bootstrap, and abandon it immediately if the app starts
	// shutting down. Shutdown cannot close this client for us: it only closes
	// the published session, and during a restart there is none.
	ctx, cancel := context.WithTimeout(context.Background(), bootstrapTimeout)
	defer cancel()
	go func() {
		select {
		case <-bounceTor.stopChannel():
			cancel()
		case <-ctx.Done():
		}
	}()

	// Report bootstrap progress while it runs. This only logs; connectivity is
	// announced later, once the hidden service can actually accept.
	stopProgress := bounceTor.logProgress(client, "bootstrapping tor")
	err = client.Bootstrap(ctx)
	stopProgress()
	if err != nil {
		client.Close()
		return nil, err
	}
	log.Info("connected to the Tor network")

	// Create an onion service to listen on any port but show as 80.
	//
	// NoWait deliberately: the listener accepts as soon as it exists, and
	// connections simply do not arrive until the descriptor is up. Blocking on
	// reachability instead would delay startup by minutes, because Arti reports
	// the service as bootstrapping until its introduction points settle, long
	// after the descriptor has been published.
	//
	// The stored private key is reused, so a restart republishes at the same
	// .onion address and peers reach us where they already expect to.
	onion, err := client.Listen(ctx, arti.OnionConfig{
		PrivateKey: bounceTor.privateKey,
		Ports:      []int{80},
		NoWait:     true,
	})
	if err != nil {
		client.Close()
		return nil, err
	}

	s := &session{client: client, onion: onion}

	// Report reachability when it arrives, without holding startup for it.
	go bounceTor.reportWhenReachable(s)

	// A first run has no key until the service exists, so persist what we got.
	if bounceTor.privateKey == nil {
		pubkey, err := onion.PublicKey()
		if err != nil {
			client.Close()
			return nil, err
		}
		bounceTor.saveHiddenServiceKey(onion.PrivateKey(), pubkey)
	}

	log.WithFields(log.Fields{
		"id": onion.ID(),
	}).Info("published hidden service")

	return s, nil
}

// publishSession makes a session visible to Accept and Dial and releases anyone
// waiting on the readiness gate.
func (bounceTor *TorNetwork) publishSession(s *session) {
	bounceTor.sessionMutex.Lock()
	bounceTor.session = s
	gate := bounceTor.ready
	if gate == nil {
		gate = make(chan struct{})
		bounceTor.ready = gate
	}
	bounceTor.sessionMutex.Unlock()

	// Closing releases every waiter at once, and works whether there are none
	// or many - unlike a send, which would need exactly one receiver.
	releaseGate(gate)
}

// releaseGate closes a readiness channel unless it is already closed.
//
// Restarts are serialised by restartMutex and Start runs before any of them, so
// there is never a second closer racing this.
func releaseGate(gate chan struct{}) {
	if gate == nil {
		return
	}
	select {
	case <-gate:
		// already released
	default:
		close(gate)
	}
}

// Restart replaces the Tor client with a fresh one against the same state
// directory, and is the blunt way to recover when Tor is up but useless.
//
// No guard failure state is persisted by Arti, so a new client starts with
// every guard retriable and no accumulated backoff - which is the effect we
// would otherwise get from GuardMgr::mark_all_guards_retriable, if that were
// reachable from an embedder.
//
// Accept and Dial park on the readiness channel while this runs, so callers see
// a pause rather than a burst of errors. The one caller already inside
// onion.Accept when the old client closes gets a non-fatal error and retries.
//
// A failed attempt leaves the network offline and returns the error; it does
// not retry. The returned error is advisory, never fatal - the usual cause is
// simply that the internet is still down - and the caller is expected to try
// again later if connectivity has not come back.
//
// It blocks for up to bootstrapTimeout. Note that the guard state this exists
// to discard is already gone by the time Tor is reached: Arti persists no guard
// failure state, so opening a fresh client wipes it, whether or not the
// bootstrap that follows succeeds.
func (bounceTor *TorNetwork) Restart() error {
	bounceTor.restartMutex.Lock()
	defer bounceTor.restartMutex.Unlock()

	if bounceTor.shuttingDown() {
		return errNetworkStopped
	}

	// Withdraw the session and install a fresh, unclosed ready channel before
	// tearing anything down, so no caller reaches into a client that is being
	// dropped. Callers park on the new channel until this restart finishes.
	bounceTor.sessionMutex.Lock()
	old := bounceTor.session
	previous := bounceTor.ready
	bounceTor.session = nil
	bounceTor.ready = make(chan struct{})
	bounceTor.sessionMutex.Unlock()

	// Release anyone still parked on the previous channel so they re-read and
	// park on the new one. Without this, a waiter left over from a restart that
	// FAILED is holding a channel nothing will ever close, and would sit there
	// for the full restartWaitLimit even after a later restart succeeded.
	// They wake once, find no session and the new channel, and park again.
	releaseGate(previous)

	log.Warn("restarting tor")
	bounceTor.setOnline(false)

	if old != nil {
		// Closing stops the event pump, closes the onion listener, and drops
		// Arti's runtime - which blocks until its tasks wind down, so the state
		// directory lock is free by the time this returns. That is what lets us
		// reopen the same DataDir immediately below.
		if err := old.client.Close(); err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Warn("error closing the previous tor client")
		}
	}

	s, err := bounceTor.openSession()
	if err != nil {
		// Give up on this attempt and stay offline. There is deliberately no
		// retry here: the caller decides when to try again, and knows whether
		// it is worth doing - it can see whether any sockets have come back.
		//
		// The ready channel is left unclosed. Closing it with no session to
		// hand out would release every waiter, send them round the loop, and
		// park them on the same already-closed channel: a hot spin until their
		// deadline. Leaving it open costs them nothing, since they stay
		// descheduled until restartWaitLimit and then return non-fatally. The
		// next Restart releases them explicitly when it installs its own
		// channel, so they do not wait out the full deadline for nothing.
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("failed to restart tor, network is offline until the next attempt")
		return err
	}

	bounceTor.publishSession(s)
	go bounceTor.watchOnlineStatus(s)
	bounceTor.setOnline(true)

	log.WithFields(log.Fields{
		"id": s.onion.ID(),
	}).Info("tor restarted")
	return nil
}

// startupFailed reports a failure to bring the network up.
//
// Closing the app while the hidden service is still publishing cancels these
// operations, and that is ordinary rather than fatal - publication routinely
// takes minutes, so a user quitting before it finishes must not be treated as
// a crash.
func (bounceTor *TorNetwork) startupFailed(err error, message string) {
	bounceTor.shutdownMutex.Lock()
	shuttingDown := bounceTor.shutdown
	bounceTor.shutdownMutex.Unlock()

	entry := log.WithFields(log.Fields{"error": err.Error()})
	if shuttingDown {
		entry.Info(message + " (cancelled by shutdown)")
		return
	}
	entry.Fatal(message)
}

// reportWhenReachable logs when Arti considers the service fully reachable.
//
// Purely informational: startup does not wait for it, because it can lag the
// descriptor going up by several minutes.
func (bounceTor *TorNetwork) reportWhenReachable(s *session) {
	started := time.Now()
	if err := s.onion.WaitPublished(context.Background()); err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Debug("stopped waiting for hidden service reachability")
		return
	}
	log.WithFields(log.Fields{
		"id":      s.onion.ID(),
		"elapsed": time.Since(started).Round(time.Second).String(),
	}).Info("hidden service is fully reachable")
}

// forwardTorLogs sends Arti's log records to the application log.
//
// Arti is the only thing that knows why a bootstrap is slow, so without this
// a stall is indistinguishable from ordinary progress.
func (bounceTor *TorNetwork) forwardTorLogs() {
	records, err := arti.EnableLogging("info")
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Warn("could not capture tor logs")
		return
	}

	go func() {
		descriptorSeen := false
		for record := range records {
			// Arti has no API for "the descriptor is up" - its status
			// aggregates the publisher with the introduction point manager and
			// reports neither alone. The publisher does announce its next
			// upload once one has succeeded, so recognising that gives an
			// accurate first-publication time. Diagnostic only: if Arti
			// rewrites the message this silently stops reporting, and nothing
			// depends on it.
			if !descriptorSeen && record.Target == "tor_hsservice::publish::reactor" &&
				strings.Contains(record.Message, "reuploading descriptor") {
				descriptorSeen = true
				log.WithFields(log.Fields{
					"id": bounceTor.Address(),
				}).Info("hidden service descriptor published")
			}

			entry := log.WithFields(log.Fields{
				"source": "tor",
				"target": record.Target,
			})
			switch record.Level {
			case "ERROR":
				entry.Error(record.Message)
			case "WARN":
				entry.Debug(record.Message) // Hide Arti logs when logging at Info level
			case "INFO":
				entry.Debug(record.Message) // Hide Arti logs when logging at Info level
			default:
				entry.Debug(record.Message)
			}
		}
	}()
}

// logProgress reports bootstrap progress until the returned function is called.
//
// Only bootstrap has a meaningful progress figure; publication does not, which
// is why nothing waits on it.
func (bounceTor *TorNetwork) logProgress(client *arti.Client, what string) func() {
	done := make(chan struct{})
	started := time.Now()

	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				status := client.Status()
				log.WithFields(log.Fields{
					"elapsed":  time.Since(started).Round(time.Second).String(),
					"progress": status.Progress,
					"status":   status.Summary,
				}).Info(what + ": still working")
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			log.WithFields(log.Fields{
				"elapsed": time.Since(started).Round(time.Second).String(),
			}).Info(what + ": done")
		})
	}
}

// watchOnlineStatus reports connectivity changes to the callbacks.
//
// This replaces polling the control port: an update is pushed whenever the
// client's bootstrap view changes, rather than up to a poll interval later.
//
// Known limitation: Ready does not retract. Arti derives it from timestamps
// written on success and never cleared, so once bootstrap succeeds it stays
// true for the life of the process - losing the network does not turn it off,
// and regaining it produces no event. In practice this reports coming online
// once at startup and never reports going offline. Restoring that needs a
// liveness signal go-arti does not currently expose.
func (bounceTor *TorNetwork) watchOnlineStatus(s *session) {
	updates := s.client.StatusUpdates()
	defer s.client.Unsubscribe(updates)

	// Subscribe first, then seed from the current status: updates only arrive
	// on a change, and by this point the client is usually already online, so
	// waiting for one would mean never reporting the state we are in.
	bounceTor.setOnline(s.client.Status().Ready)

	for status := range updates {
		log.WithFields(log.Fields{
			"progress": status.Progress,
			"ready":    status.Ready,
			"summary":  status.Summary,
		}).Trace("tor status changed")
		bounceTor.setOnline(status.Ready)
	}
}

// setOnline notifies the callbacks when connectivity changes state.
func (bounceTor *TorNetwork) setOnline(online bool) {
	torLock.Lock()
	defer torLock.Unlock()

	if online == bounceTor.online {
		return
	}
	bounceTor.online = online
	if online {
		bounceTor.callbacks.NetworkOnline()
	} else {
		bounceTor.callbacks.NetworkOffline()
	}
}

func (bounceTor *TorNetwork) Address() string {
	// If the network is online, get the ID from the service
	// Peek rather than wait: this is called from log forwarding and from the
	// UI, neither of which should block for a restart.
	if s := bounceTor.peekSession(); s != nil {
		return s.onion.ID()
	}

	// If the network is offline, derive the address from the public key on disk
	pubkey, _ := bounceTor.hiddenServiceKey()
	if pubkey == nil {
		return ""
	}
	id, err := arti.OnionIDFromPublicKey(pubkey)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("stored public key is not a valid onion identity")
		return ""
	}
	return id
}

func (bounceTor *TorNetwork) Accept() (net.Conn, error, bool) {
	// Wait for a live session rather than failing fast: the accept loop retries
	// non-fatal errors with no backoff, so returning immediately during a
	// restart would spin.
	s, err := bounceTor.awaitSession()
	if err != nil {
		// Shutdown is terminal for the accept loop, and must be reported as
		// fatal to stop it: the caller only leaves the loop on a fatal error,
		// and treats one during shutdown as a clean exit. Reporting it as
		// non-fatal spins, because once the network is stopped every
		// subsequent call returns immediately with the same error.
		return nil, err, errors.Is(err, errNetworkStopped)
	}

	connection, err := s.onion.Accept()
	if err != nil {
		// Shutting down: same reasoning as above, stop the loop.
		if bounceTor.shuttingDown() {
			return nil, errNetworkStopped, true
		}
		// A restart replaced our session underneath us. That is not a failure,
		// so report it non-fatally and let the caller come back and wait for
		// the new listener.
		if bounceTor.superseded(s) {
			return nil, errNetworkRestarting, false
		}
		return nil, err, true
	}

	// Handshake with the connection to learn the remote address
	challenge := make([]byte, handshakeChallengeSize)
	n, err := rand.Read(challenge)
	if n != handshakeChallengeSize {
		return nil, errors.New("failed to generate random challenge for handshake"), false
	}
	if err != nil {
		return nil, err, false
	}
	err = write(connection, challenge)
	if err != nil {
		return nil, err, false
	}

	// All onion IDs will be the same size, read the number of bytes that correspond to our ID
	peerAddress, err := read(connection, len(s.onion.ID()))
	if err != nil {
		return nil, err, false
	}

	// Read their bytes they XOR'd with our challenge
	challengeXor, err := read(connection, handshakeChallengeSize)
	if err != nil {
		return nil, err, false
	}

	// Read their signature of the challenge
	response, err := read(connection, signatureSize)
	if err != nil {
		return nil, err, false
	}

	ok := bounceTor.VerifySignature(string(peerAddress), xor(challenge, challengeXor), response)
	if !ok {
		return nil, errors.New("signature validation failed during handshake with " + string(peerAddress)), false
	}

	torConn := &torNetworkConnection{
		underlying: connection,
		localAddress: &torAddress{
			address: s.onion.ID(),
		},
		remoteAddress: &torAddress{
			address: string(peerAddress),
		},
	}
	return torConn, nil, false
}

func (bounceTor *TorNetwork) Dial(address string) (net.Conn, error) {
	// Dialing no longer needs to be serialised against the connectivity check:
	// that used to share the control port, and now arrives as an event.
	// Same gate as Accept. A dial that arrives mid-restart waits for the new
	// client instead of failing, and callers that cannot wait get
	// errNetworkRestarting after restartWaitLimit, which they treat like any
	// other failed dial.
	s, err := bounceTor.awaitSession()
	if err != nil {
		return nil, err
	}

	conn, err := s.client.DialContext(context.Background(), "tcp", address+".onion:80")
	if err != nil {
		return nil, err
	}

	// Handshake
	challenge, err := read(conn, handshakeChallengeSize)
	if err != nil {
		return nil, err
	}

	challengeXor := make([]byte, handshakeChallengeSize)
	n, err := rand.Read(challengeXor)
	if n != handshakeChallengeSize {
		return nil, errors.New("failed to generate random challenge for handshake XOR")
	}
	if err != nil {
		return nil, err
	}

	finalChallenge := xor(challenge, challengeXor)

	response := bounceTor.Sign(finalChallenge)

	err = write(conn, []byte(s.onion.ID()))
	if err != nil {
		return nil, err
	}

	err = write(conn, challengeXor)
	if err != nil {
		return nil, err
	}

	err = write(conn, response)
	if err != nil {
		return nil, err
	}

	torConn := &torNetworkConnection{
		underlying: conn,
		localAddress: &torAddress{
			address: s.onion.ID(),
		},
		remoteAddress: &torAddress{
			address: address,
		},
	}
	return torConn, nil
}

func (bounceTor *TorNetwork) Sign(data []byte) []byte {
	signature, err := arti.Sign(bounceTor.privateKey, data)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("failed to sign with the hidden service key")
		return nil
	}
	return signature
}

func (bounceTor *TorNetwork) VerifySignature(address string, data []byte, signature []byte) bool {
	publicKey, err := arti.PublicKeyFromOnionID(address)
	if err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Error("invalid address passed to VerifySignature")
		return false
	}
	return arti.Verify(publicKey, data, signature)
}

func (bounceTor *TorNetwork) Shutdown() {
	bounceTor.shutdownMutex.Lock()
	if bounceTor.shutdown {
		// We're already shutting down, there's no need to enter this
		// function again, and doing so can cause segfaults.
		bounceTor.shutdownMutex.Unlock()
		return
	}
	bounceTor.shutdown = true
	bounceTor.shutdownMutex.Unlock()

	// Release Start, and anything else waiting on the network.
	stop := bounceTor.stopChannel()
	bounceTor.stoppedOnce.Do(func() { close(stop) })

	log.Info("shutting down tor")

	// Take the session away first, so nothing new reaches into a client we are
	// about to drop. Anyone already waiting is released by stopChannel above.
	bounceTor.sessionMutex.Lock()
	current := bounceTor.session
	bounceTor.session = nil
	bounceTor.sessionMutex.Unlock()

	if current == nil {
		// Network never fully started and we're already closing the app
		log.Warn("stopping tor before the hidden service was published")
	} else {
		// Closing the client also closes the onion service's listener, so there
		// is no separate teardown for it.
		if err := current.client.Close(); err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error stopping tor")
		}
	}
	log.Info("tor stopped")
}

//
// torNetworkConnection is an implementation of net.Conn that allows us to specify the onion ID as the remote and local address
//

type torNetworkConnection struct {
	underlying    net.Conn
	localAddress  net.Addr
	remoteAddress net.Addr
}

func (conn *torNetworkConnection) Read(b []byte) (int, error) {
	return conn.underlying.Read(b)
}

func (conn *torNetworkConnection) Write(b []byte) (int, error) {
	return conn.underlying.Write(b)
}

func (conn *torNetworkConnection) Close() error {
	return conn.underlying.Close()
}

func (conn *torNetworkConnection) LocalAddr() net.Addr {
	return conn.localAddress
}

func (conn *torNetworkConnection) RemoteAddr() net.Addr {
	return conn.remoteAddress
}

func (conn *torNetworkConnection) SetDeadline(t time.Time) error {
	return conn.underlying.SetDeadline(t)
}

func (conn *torNetworkConnection) SetReadDeadline(t time.Time) error {
	return conn.underlying.SetReadDeadline(t)
}

func (conn *torNetworkConnection) SetWriteDeadline(t time.Time) error {
	return conn.underlying.SetWriteDeadline(t)
}

type torAddress struct {
	address string
}

func (ta *torAddress) Network() string {
	return "tor"
}

func (ta *torAddress) String() string {
	return ta.address
}

//
// Wire primatives for the handshake
//

func read(conn net.Conn, size int) ([]byte, error) {
	payload := make([]byte, 0)
	payloadRead := 0
	for payloadRead < size {
		buf := make([]byte, size-payloadRead)
		n, err := conn.Read(buf)
		payloadRead += n
		if err == io.EOF {
			if payloadRead != size {
				return []byte{}, err
			}
		} else if err != nil {
			return []byte{}, err
		}
		payload = append(payload, buf[:n]...)
	}
	return payload, nil
}

func write(conn net.Conn, payload []byte) error {
	bytesToWrite := len(payload)
	bytesWritten := 0
	for bytesWritten < bytesToWrite {
		n, err := conn.Write(payload[bytesWritten:])
		if err != nil {
			return err
		}
		bytesWritten += n
	}
	return nil
}

func xor(a, b []byte) []byte {
	if len(a) != len(b) {
		log.Fatal("cannot XOR byte slices of different length")
	}
	n := len(a)

	dst := make([]byte, n)
	for i := 0; i < n; i++ {
		dst[i] = a[i] ^ b[i]
	}
	return dst
}
