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

type TorNetwork struct {
	routerDirectory string
	keyDirectory    string
	onion           *arti.OnionService
	tor             *arti.Client
	callbacks       chat.NetworkCallbacks
	publicKey       ed25519.PublicKey
	privateKey      []byte
	online          bool
	shutdown        bool
	shutdownMutex   sync.Mutex

	// stopped is closed by Shutdown, releasing Start.
	stopped     chan struct{}
	stoppedOnce sync.Once
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

	var err error
	bounceTor.tor, err = arti.Open(arti.Config{DataDir: bounceTor.routerDirectory})
	if err != nil {
		bounceTor.startupFailed(err, "failed to start TOR")
		return
	}

	ctx := context.Background()

	// Report bootstrap progress while it runs. This only logs; connectivity is
	// announced later, once the hidden service can actually accept.
	stopProgress := bounceTor.logProgress("bootstrapping tor")
	err = bounceTor.tor.Bootstrap(ctx)
	stopProgress()
	if err != nil {
		bounceTor.startupFailed(err, "failed to connect to the Tor network")
		return
	}
	log.Info("connected to the Tor network")

	// Create an onion service to listen on any port but show as 80.
	//
	// NoWait deliberately: the listener accepts as soon as it exists, and
	// connections simply do not arrive until the descriptor is up. Blocking on
	// reachability instead would delay startup by minutes, because Arti reports
	// the service as bootstrapping until its introduction points settle, long
	// after the descriptor has been published.
	bounceTor.onion, err = bounceTor.tor.Listen(ctx, arti.OnionConfig{
		PrivateKey: bounceTor.privateKey,
		Ports:      []int{80},
		NoWait:     true,
	})
	if err != nil {
		bounceTor.startupFailed(err, "failed to create TOR hidden service")
		return
	}

	// Report reachability when it arrives, without holding startup for it.
	go bounceTor.reportWhenReachable()

	// A first run has no key until the service exists, so persist what we got.
	if bounceTor.privateKey == nil {
		pubkey, err := bounceTor.onion.PublicKey()
		if err != nil {
			bounceTor.startupFailed(err, "published service has an unusable key")
			return
		}
		bounceTor.saveHiddenServiceKey(bounceTor.onion.PrivateKey(), pubkey)
	}

	log.WithFields(log.Fields{
		"id": bounceTor.onion.ID(),
	}).Info("published hidden service")

	// Only now report connectivity. NetworkOnline is what starts the accept
	// loop, so announcing it any earlier hands out a service that does not yet
	// exist to accept on.
	go bounceTor.watchOnlineStatus()

	// Start blocks for the lifetime of the network, as it did before, but
	// returns on shutdown rather than leaving the goroutine parked forever.
	<-bounceTor.stopChannel()
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
func (bounceTor *TorNetwork) reportWhenReachable() {
	started := time.Now()
	if err := bounceTor.onion.WaitPublished(context.Background()); err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Debug("stopped waiting for hidden service reachability")
		return
	}
	log.WithFields(log.Fields{
		"id":      bounceTor.onion.ID(),
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
func (bounceTor *TorNetwork) logProgress(what string) func() {
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
				status := bounceTor.tor.Status()
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
func (bounceTor *TorNetwork) watchOnlineStatus() {
	updates := bounceTor.tor.StatusUpdates()
	defer bounceTor.tor.Unsubscribe(updates)

	// Subscribe first, then seed from the current status: updates only arrive
	// on a change, and by this point the client is usually already online, so
	// waiting for one would mean never reporting the state we are in.
	bounceTor.setOnline(bounceTor.tor.Status().Ready)

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
	if bounceTor.onion != nil {
		return bounceTor.onion.ID()
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
	if bounceTor.onion == nil {
		// Callers treat a panic here as fatal, so refuse clearly instead.
		return nil, errors.New("cannot accept before the hidden service is published"), true
	}
	connection, err := bounceTor.onion.Accept()
	if err != nil {
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
	peerAddress, err := read(connection, len(bounceTor.onion.ID()))
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
			address: bounceTor.onion.ID(),
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
	if bounceTor.tor == nil || bounceTor.onion == nil {
		// Technically we don't need to wait for the hidden service to be published before we can dial,
		// but any failures to publish indicate a major problem
		return nil, errors.New("cannot dial while network is not started")
	}

	conn, err := bounceTor.tor.DialContext(context.Background(), "tcp", address+".onion:80")
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

	err = write(conn, []byte(bounceTor.onion.ID()))
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
			address: bounceTor.onion.ID(),
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
	if bounceTor.onion == nil {
		// Network never fully started and we're already closing the app
		log.Warn("stopping hidden service before hidden service was published")
	} else {
		// Stop the hidden service
		err := bounceTor.onion.Close()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Error("error stopping hidden service")
		}
	}
	if bounceTor.tor == nil {
		// Network never fully started and we're already closing the app
		log.Warn("stopping tor before tor has fully started")
	} else {
		// Stop Tor
		err := bounceTor.tor.Close()
		if err != nil {
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
