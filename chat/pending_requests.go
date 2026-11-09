package chat

import (
	"sync"
	"time"
)

// pendingRequests is the set of peers we have sent a request to and are still willing to hear back from.  Both the
// add-user and the sync-device flows keep one: a request is recorded when it is sent, an answer is only honoured if
// it comes from a peer we asked and within the window the offer itself was valid for, and the first answer clears
// the entry.  Anything else, an answer we never asked for or one that arrives long after we gave up, is ignored
// instead of being shown to the user as the fate of their request.
type pendingRequests struct {
	sync.Mutex
	window time.Duration
	sent   map[string]time.Time
}

func newPendingRequests(window time.Duration) *pendingRequests {
	return &pendingRequests{
		window: window,
		sent:   map[string]time.Time{},
	}
}

// expectFrom records that a request has just been sent to the peer, and forgets requests nobody can answer anymore
func (p *pendingRequests) expectFrom(peer string) {
	p.Lock()
	defer p.Unlock()

	for other, at := range p.sent {
		if time.Since(at) > p.window {
			delete(p.sent, other)
		}
	}

	p.sent[peer] = time.Now()
}

// isExpectedFrom reports whether an answer from the peer is one we asked for and are still waiting on
func (p *pendingRequests) isExpectedFrom(peer string) bool {
	p.Lock()
	defer p.Unlock()

	at, ok := p.sent[peer]
	delete(p.sent, peer)

	return ok && time.Since(at) <= p.window
}

var pendingAddUserRequests = newPendingRequests(time.Duration(addUserOfferValidForSeconds) * time.Second)
var pendingSyncRequests = newPendingRequests(time.Duration(syncDeviceOfferValidForSeconds) * time.Second)

// Secrets that have been claimed, so a request that re-uses one is ignored rather than refused.  A secret only
// needs remembering for as long as its offer could have been valid.
var usedSecrets = map[string]time.Time{}
var usedSecretsMutex sync.Mutex

func secretRecentlyUsed(key string) bool {
	usedSecretsMutex.Lock()
	defer usedSecretsMutex.Unlock()

	_, used := usedSecrets[key]
	return used
}

func markSecretUsed(key string, window time.Duration) {
	usedSecretsMutex.Lock()
	defer usedSecretsMutex.Unlock()

	for other, at := range usedSecrets {
		if time.Since(at) > window {
			delete(usedSecrets, other)
		}
	}

	usedSecrets[key] = time.Now()
}
