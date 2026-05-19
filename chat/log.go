package chat

import (
	"github.com/DeRuina/timberjack"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
)

var logfile *timberjack.Logger

var nocolor = logrus.TextFormatter{DisableColors: true}

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
	line, err := nocolor.Format(e)
	if err == nil {
		logfile.Write(line)
	}
	return nil
}
