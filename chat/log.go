package chat

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"regexp"

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
	return []log.Level{
		log.PanicLevel,
		log.FatalLevel,
		log.ErrorLevel,
		log.WarnLevel,
		log.InfoLevel,
		log.DebugLevel,
		log.TraceLevel,
	}
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
