// Package logging initializes the one global logger. It is a cross-cutting
// concern, so it is never injected into a constructor.
package logging

import (
	"os"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Init configures the global logger. Development gets a human-readable console
// writer; production gets JSON on stdout.
func Init(level string, production bool) {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix

	parsed, err := zerolog.ParseLevel(level)
	if err != nil {
		parsed = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(parsed)

	if production {
		log.Logger = zerolog.New(os.Stdout).With().Timestamp().Caller().Logger()
		return
	}
	log.Logger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).
		With().Timestamp().Caller().Logger()
}
