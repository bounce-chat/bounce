package chat

import (
	"errors"
	"fmt"
	"strings"
)

// A sync string is the out-of-band pairing code one device shows another to add it as a contact, a sync device, or
// the manager of an encrypted device.  Its grammar is:
//
//	<network>://<address>:<secret>
//
// The network scheme names the overlay the receiving host must dial us on.  It is carried here (and, separately, as
// a hashed field on the device record) so the network is explicit everywhere; it is never folded into the address
// string itself, which stays the bare locator that gets signed and recovered back into the device's key.
//
// The network name is owned by the network provider (tor.TorNetwork.Name()); the engine never hardcodes a wire
// value for it and always reads it back through b.network.Name().

// buildSyncString renders "<network>://<address>:<secret>" for our own address and the given secret.
func (b *Bounce) buildSyncString(secret string) string {
	return b.network.Name() + "://" + b.network.Address() + ":" + secret
}

// parseSyncString splits a pairing code into its address and secret.  The scheme is mandatory, and a code for a
// network this build cannot dial is refused cleanly here rather than mis-parsed — which is what lets an older build
// reject a newer network's code instead of mishandling it.  (Today we dial exactly our own network; when more than
// one network can be dialed this check widens to the set of dialable networks.)
func (b *Bounce) parseSyncString(data string) (address string, secret string, err error) {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(data), "://")
	if !ok {
		return "", "", errors.New("invalid sync string: missing network scheme")
	}
	if scheme != b.network.Name() {
		return "", "", fmt.Errorf("unsupported network %q", scheme)
	}
	address, secret, ok = strings.Cut(rest, ":")
	if !ok || address == "" || secret == "" {
		return "", "", errors.New("invalid sync string")
	}
	return address, secret, nil
}
