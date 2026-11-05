package network

import (
	"context"
	"errors"
	"net"
	"time"

	log "github.com/sirupsen/logrus"
)

// Liveness.
//
// Arti cannot say whether the network is still there: its Ready flag never retracts.  So the transport finds out
// for itself.  Once a session is up it dials its own onion address and holds that connection open, writing a byte
// down the dialled end every canaryKeepAliveInterval and reading those bytes back on the end our own listener
// accepted.  A rendezvous to ourselves exercises guards, circuits, the directory and the hidden-service path end to
// end, with no third party and nothing outside Tor.  When the canary dies it is redialled; when it cannot be
// re-established within canaryRedialWindow, and no peer connection has read anything in canaryReadWindow either,
// the network is declared dead and recovered by restarting Tor.
//
// Peer traffic vetoes the verdict because Tor tears circuits down routinely, and a guard or directory change can
// stop new connections for a few minutes while established ones keep working: a dead canary beside live peers means
// "cannot make new connections right now", not an outage.  A device alone on the network has no peers to vouch for
// it, and there the canary alone decides, which is the case this exists for.
//
// The canary belongs to its session: it runs on that session's client, its accepting end arrives through that
// session's accept pump, and it stops when the session does.  Recovery restarts Tor until a session comes up whose
// canary connects; a bootstrap that fails costs no data (the internet is down) and is retried at a fixed interval,
// while a bootstrap that succeeds without the canary ever connecting has downloaded the directory for nothing, and
// only that case backs off.
//
// Durations are vars so tests can shorten them; nothing else assigns them.
var (
	// canaryDelay is the wait after a session comes up before the first self-dial.  It is also the grace period
	// for the hidden service descriptor to become fetchable, which takes minutes after publication.
	canaryDelay = 3 * time.Minute

	// canaryKeepAliveInterval is how often a byte is written down the canary.
	canaryKeepAliveInterval = 15 * time.Second

	// canaryReadWindow is how long the accepting end waits for a byte before the canary is dead, and how recently a
	// peer connection must have read something to vouch for the network.  Matches the engine's socket liveness.
	canaryReadWindow = 60 * time.Second

	// canaryRedialWindow is how long the canary keeps trying to establish itself before giving up on the session.
	canaryRedialWindow = 5 * time.Minute

	// canaryRedialPause is the wait between dial attempts inside that window.
	canaryRedialPause = 30 * time.Second

	// canaryDialTimeout bounds one dial of our own address, rendezvous included.
	canaryDialTimeout = 60 * time.Second

	// restartInterval is the pause between restart attempts while bootstrap keeps failing.
	restartInterval = 3 * time.Minute

	// restartBackoffLimit caps the growing pause before restarting a session that bootstrapped but whose canary
	// never connected.
	restartBackoffLimit = time.Hour
)

// The byte the canary carries.  Its value does not matter.
const canaryByte = 0x42

var errCanaryNotAccepted = errors.New("our listener never accepted the canary connection")

// canaryEnds is one canary connection: the end we dialled and the end our listener accepted
type canaryEnds struct {
	dialled  net.Conn
	accepted net.Conn
}

// runCanary watches one session for its lifetime and gives up on it when the network appears to be dead
func (bounceTor *TorNetwork) runCanary(s *session) {
	bounceTor.runCanaryWith(
		func() (net.Conn, error) { return bounceTor.dialSelf(s) },
		s.canaryAccepted,
		s.stopping,
		func(confirmed bool) { bounceTor.networkDead(s, confirmed) },
	)
}

// dialSelf dials this session's own onion address and runs the dialer half of the handshake, the way Dial does for a
// peer.  The result is held by the canary and never handed to the engine, and its bytes are not peer traffic.
func (bounceTor *TorNetwork) dialSelf(s *session) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), canaryDialTimeout)
	defer cancel()

	address := s.onion.ID()
	raw, err := s.client.DialContext(ctx, "tcp", address+".onion:80")
	if err != nil {
		return nil, err
	}
	conn, err := bounceTor.handshakeDialer(address, address, raw)
	if err != nil {
		return nil, err
	}
	conn.tracker = nil
	return conn, nil
}

// runCanaryWith is runCanary with its dependencies passed in.  dial establishes the dialled end; the accepting end
// arrives on accepted, routed there by the accept pump; stopping ends the canary with its session; dead reports that
// the session has been given up on, and whether a canary ever connected on it.
func (bounceTor *TorNetwork) runCanaryWith(
	dial func() (net.Conn, error),
	accepted <-chan *torNetworkConnection,
	stopping <-chan struct{},
	dead func(confirmed bool),
) {
	if !bounceTor.sleepUnless(canaryDelay, stopping) {
		return
	}

	confirmed := false
	for {
		ends, stopped := bounceTor.establishCanary(dial, accepted, stopping)
		if stopped {
			return
		}
		if ends == nil {
			if bounceTor.peersRecentlyRead() {
				log.Warn("cannot connect to our own hidden service, but peers are still talking to us; not restarting")
				if !bounceTor.sleepUnless(canaryRedialPause, stopping) {
					return
				}
				continue
			}
			log.Warn("cannot connect to our own hidden service and no peer has spoken recently, the network appears to be down")
			dead(confirmed)
			return
		}

		confirmed = true
		bounceTor.restartBackoff.Store(0)
		log.Info("connected to our own hidden service, the network is reachable")
		if bounceTor.holdCanary(ends, stopping) {
			return
		}
		log.Warn("the connection to our own hidden service died, reconnecting")
	}
}

// establishCanary dials until a canary is up or canaryRedialWindow has passed.  Returns no ends when it gave up, and
// stopped when the session ended first.
func (bounceTor *TorNetwork) establishCanary(
	dial func() (net.Conn, error),
	accepted <-chan *torNetworkConnection,
	stopping <-chan struct{},
) (ends *canaryEnds, stopped bool) {
	deadline := time.Now().Add(canaryRedialWindow)
	for {
		// An accepting end left over from an earlier attempt belongs to a dialled end that is gone
		for drained := false; !drained; {
			select {
			case stale := <-accepted:
				stale.Close()
			default:
				drained = true
			}
		}

		dialled, err := dial()
		if err == nil {
			select {
			case acceptedEnd := <-accepted:
				return &canaryEnds{dialled: dialled, accepted: acceptedEnd}, false
			case <-time.After(handshakeTimeout):
				dialled.Close()
				err = errCanaryNotAccepted
			case <-stopping:
				dialled.Close()
				return nil, true
			case <-bounceTor.stopChannel():
				dialled.Close()
				return nil, true
			}
		}
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Debug("could not connect to our own hidden service")

		if time.Now().After(deadline) {
			return nil, false
		}
		if !bounceTor.sleepUnless(canaryRedialPause, stopping) {
			return nil, true
		}
	}
}

// holdCanary keeps one canary alive until it dies, returning whether the session stopped instead.  The dialled end
// carries a byte every canaryKeepAliveInterval; the accepted end must see one within every canaryReadWindow.
func (bounceTor *TorNetwork) holdCanary(ends *canaryEnds, stopping <-chan struct{}) (stopped bool) {
	// Both ends are closed on the way out, and the reader is waited for, so nothing of this canary outlives it
	readerDone := make(chan struct{})
	defer func() {
		ends.dialled.Close()
		ends.accepted.Close()
		<-readerDone
	}()

	readFailed := make(chan error, 1)
	go func() {
		defer close(readerDone)
		buf := make([]byte, 1)
		for {
			if err := ends.accepted.SetReadDeadline(time.Now().Add(canaryReadWindow)); err != nil {
				readFailed <- err
				return
			}
			if _, err := ends.accepted.Read(buf); err != nil {
				readFailed <- err
				return
			}
		}
	}()

	ticker := time.NewTicker(canaryKeepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			ends.dialled.SetWriteDeadline(time.Now().Add(canaryReadWindow))
			if _, err := ends.dialled.Write([]byte{canaryByte}); err != nil {
				log.WithFields(log.Fields{
					"error": err.Error(),
				}).Debug("could not write to our own hidden service")
				return false
			}
		case err := <-readFailed:
			log.WithFields(log.Fields{
				"error": err.Error(),
			}).Debug("stopped hearing from ourselves over our own hidden service")
			return false
		case <-stopping:
			return true
		case <-bounceTor.stopChannel():
			return true
		}
	}
}

// peersRecentlyRead reports whether any peer connection read something within the last canaryReadWindow
func (bounceTor *TorNetwork) peersRecentlyRead() bool {
	last := bounceTor.lastPeerRead.Load()
	return last != 0 && time.Since(time.Unix(0, last)) < canaryReadWindow
}

// networkDead is the canary's verdict on its session.  One recovery runs at a time, and a session that has already
// been replaced needs none.
func (bounceTor *TorNetwork) networkDead(s *session, confirmed bool) {
	if bounceTor.superseded(s) || bounceTor.shuttingDown() {
		return
	}
	if !bounceTor.recovering.CompareAndSwap(false, true) {
		return
	}
	go bounceTor.recoverNetwork(confirmed, bounceTor.Restart, func(d time.Duration) bool {
		return bounceTor.sleepUnless(d, nil)
	})
}

// recoverNetwork restarts Tor until a session comes up.  It returns as soon as a bootstrap succeeds: the new
// session's canary decides whether that recovery held, and calls back in here if not.
//
// confirmed says whether the session being given up on ever had a working canary.  One that did was healthy and
// simply died, so it is restarted at once.  One that never did bootstrapped but could not reach its own service: the
// directory was downloaded for nothing, and that is the case that is rate limited, waiting longer each time in a
// row up to restartBackoffLimit.  A bootstrap that fails is the internet being down, which costs nothing, so it is
// retried every restartInterval with no backoff and the backoff is reset, as it is when a canary connects.
func (bounceTor *TorNetwork) recoverNetwork(confirmed bool, restart func() error, sleep func(time.Duration) bool) {
	defer bounceTor.recovering.Store(false)
	bounceTor.setOnline(false)

	if !confirmed {
		backoff := time.Duration(bounceTor.restartBackoff.Load())
		if backoff == 0 {
			backoff = restartInterval
		} else {
			backoff *= 2
			if backoff > restartBackoffLimit {
				backoff = restartBackoffLimit
			}
		}
		bounceTor.restartBackoff.Store(int64(backoff))
		log.WithFields(log.Fields{
			"wait": backoff.String(),
		}).Warn("tor came up but our hidden service is unreachable, waiting before restarting")
		if !sleep(backoff) {
			return
		}
	}

	for {
		err := restart()
		if err == nil {
			return
		}
		if errors.Is(err, errNetworkStopped) {
			return
		}
		bounceTor.restartBackoff.Store(0)
		log.WithFields(log.Fields{
			"error": err.Error(),
			"wait":  restartInterval.String(),
		}).Info("tor could not bootstrap, retrying")
		if !sleep(restartInterval) {
			return
		}
	}
}

// sleepUnless waits for d, returning false if the session (when given) or the network stopped first
func (bounceTor *TorNetwork) sleepUnless(d time.Duration, stopping <-chan struct{}) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-stopping:
		return false
	case <-bounceTor.stopChannel():
		return false
	}
}
