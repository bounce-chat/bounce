package chat

import (
	"strings"
	"testing"

	"github.com/alecthomas/assert/v2"
	log "github.com/sirupsen/logrus"
)

// The log file on disk must not be a record of who we talk to.  Addresses are pseudonymised wherever they appear and
// fields carrying things people wrote are redacted, before a line is written.

func TestTheLogFileNamesNoAddresses(t *testing.T) {
	alice := newTestnet().Address()
	bob := newTestnet().Address()

	entry := log.WithFields(log.Fields{
		"peer":              alice,
		"encrypted_devices": alice + "," + bob,
		"some_new_field":    "introduced by " + bob,
		"count":             3,
	})
	entry.Message = "handling a frame from " + alice
	entry.Level = log.WarnLevel
	sanitised := sanitizeLogEntry(entry)
	line, err := nocolor.Format(sanitised)
	assert.NoError(t, err)
	text := string(line)

	assert.True(t, strings.HasPrefix(text, "time="), text)
	assert.True(t, strings.Contains(text, "level=warning"), "the level survives sanitising: "+text)
	assert.False(t, strings.Contains(text, alice), "an address in a known field is gone")
	assert.False(t, strings.Contains(text, bob), "and in a list, and in a field nobody listed, and in the message")
	assert.True(t, strings.Contains(text, "count=3"), "ordinary fields are untouched")

	// The same address always gets the same pseudonym within a run, so lines still correlate
	aliceName := addressPseudonym(alice)
	assert.Equal(t, 3, strings.Count(text, aliceName), "once in the peer field, once in the list, once in the message")
	assert.Equal(t, 2, strings.Count(text, addressPseudonym(bob)), "once in the list, once in the unlisted field")
	assert.NotEqual(t, aliceName, addressPseudonym(bob))

	// Console output is what live debugging uses and is left alone
	original, _ := nocolor.Format(entry)
	assert.True(t, strings.Contains(string(original), alice))
}

func TestSanitisingCanBeSwitchedOffForDebugging(t *testing.T) {
	address := newTestnet().Address()
	entry := log.WithFields(log.Fields{"peer": address, "name": "Samantha"})

	// The hook decides per entry, so the switch can be flipped here without restarting anything
	unsanitizedLogs = true
	t.Cleanup(func() { unsanitizedLogs = false })
	hook := &filehook{}

	// Format the entry the way the hook would in each mode
	scrubbed, _ := nocolor.Format(sanitizeLogEntry(entry))
	raw, _ := nocolor.Format(entry)
	assert.False(t, strings.Contains(string(scrubbed), address))
	assert.True(t, strings.Contains(string(raw), address))
	assert.True(t, strings.Contains(string(raw), "Samantha"))

	// And the hook itself writes without error in the unsanitised mode
	assert.NoError(t, hook.Fire(entry))
}

func TestTheLogFileCarriesNothingPeopleWrote(t *testing.T) {
	entry := log.WithFields(log.Fields{
		"name":  "Samantha",
		"text":  "see you at the usual place",
		"alias": "Sam",
		"notes": "met at the conference",
		"id":    "not sensitive",
	})
	line, err := nocolor.Format(sanitizeLogEntry(entry))
	assert.NoError(t, err)
	text := string(line)
	for _, written := range []string{"Samantha", "usual place", "Sam\"", "conference"} {
		assert.False(t, strings.Contains(text, written))
	}
	assert.True(t, strings.Contains(text, "id=\"not sensitive\""))
	assert.Equal(t, 4, strings.Count(text, redactedLogValue))
}
