package chat

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"
	"sync"
	"sync/atomic"

	"github.com/bounce-chat/bounce/config"

	"github.com/DeRuina/timberjack"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"github.com/zeebo/blake3"
)

const redactedLogValue = "[redacted]"

var unsanitizedLogs = os.Getenv("UNSANITIZED_LOGS") == "true"
var nocolor = logrus.TextFormatter{DisableColors: true}
var onionAddressPattern = regexp.MustCompile(`[a-z2-7]{56}`)

var logfile = &timberjack.Logger{
	Filename:           config.GetConfigDirectory() + "/bounce-log.txt",
	FileMode:           0600,
	MaxAge:             3,
	Compression:        "none",
	LocalTime:          true,
	RotateAt:           []string{"00:00", "12:00"},
	BackupTimeFormat:   "2006-01-02-15-04-05",
	AppendTimeAfterExt: true,
}

var sensitiveLogFields = map[string]bool{
	"name":  true,
	"text":  true,
	"alias": true,
	"notes": true,
}

var pseudonymKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		log.WithFields(log.Fields{
			"error": err.Error(),
		}).Fatal("error generating log pseudonym key")
	}
	return key
}()

func addressPseudonym(address string) string {
	hasher := blake3.New()
	hasher.Write(pseudonymKey)
	hasher.Write([]byte(address))
	return "onion~" + hex.EncodeToString(hasher.Sum(nil))[:8]
}

func sanitizeLogText(text string) string {
	return onionAddressPattern.ReplaceAllStringFunc(text, addressPseudonym)
}

func sanitizeLogEntry(e *log.Entry) *log.Entry {
	// Dup copies the time, caller and fields but not the level or the message, and a zero level is panic
	sanitised := e.Dup()
	sanitised.Level = e.Level
	sanitised.Message = sanitizeLogText(e.Message)
	for key, value := range e.Data {
		if sensitiveLogFields[key] {
			sanitised.Data[key] = redactedLogValue
			continue
		}
		sanitised.Data[key] = sanitizeLogText(fmt.Sprint(value))
	}
	return sanitised
}

type filehook struct{}

func (h *filehook) Levels() []log.Level {
	return log.AllLevels
}

func (h *filehook) Fire(e *log.Entry) error {
	if !unsanitizedLogs {
		e = sanitizeLogEntry(e)
	}
	line, err := nocolor.Format(e)
	if err == nil {
		logfile.Write(line)
	}
	return nil
}

// The process is shutting down once at least one engine has begun to and none remains that has not.  Shutdown gives
// up waiting on each of its stages after a couple of seconds, so work it left behind keeps running into a closed
// database, and the convention that a database error is Fatal would turn a clean quit into an exit(1) from whichever
// straggler got there first.  Rather than teaching every path in the engine to notice a shutdown, the hook below
// catches the Fatal itself.
//
// These counts are process-wide where Bounce.shutdownStarted is per engine, because a logrus hook cannot tell which
// engine logged.  In production one engine runs per process and the two views always agree; in a test binary several
// engines share a process, and counting is what keeps one engine's shutdown from muffling a real Fatal in another.
var runningEngines atomic.Int32
var engineShutdowns atomic.Int32

func processShuttingDown() bool {
	return engineShutdowns.Load() > 0 && runningEngines.Load() == 0
}

var logHooksInstalled sync.Once

// installLogHooks registers the engine's logrus hooks once per process.  Both are stateless and write to package-level
// state, so every engine opened in a process, test binaries included, shares the same two.  The shutdown hook goes
// first so that a Fatal during shutdown reaches the log file as the warning it is turned into.
func installLogHooks() {
	logHooksInstalled.Do(func() {
		log.AddHook(&shutdownFatalHook{})
		log.AddHook(&filehook{})
	})
}

// A shutdownFatalHook turns a Fatal logged after shutdown has begun into a warning and then parks the goroutine that
// logged it.  Hooks run on the logging goroutine before the entry is written and before Fatal calls Exit, so the
// line never appears as fatal and the process is never exited from here; it ends when Shutdown returns.  The
// goroutine was about to end the process anyway, so nothing is lost by never letting it continue.
type shutdownFatalHook struct{}

func (h *shutdownFatalHook) Levels() []log.Level {
	return []log.Level{log.FatalLevel}
}

func (h *shutdownFatalHook) Fire(e *log.Entry) error {
	if !processShuttingDown() {
		return nil
	}
	log.WithFields(e.Data).WithField("during_shutdown", true).Warn(e.Message)
	select {}
}
