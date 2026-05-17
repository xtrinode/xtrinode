package cmdflags

import (
	"flag"
	"fmt"
	"strings"

	"go.uber.org/zap/zapcore"
	ctrlzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// WasSet reports whether a flag was explicitly provided by the caller.
func WasSet(fs *flag.FlagSet, name string) bool {
	if fs == nil {
		return false
	}
	wasSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

// ApplyLogLevelFlag maps the project-level --log-level flag onto controller-runtime zap options.
func ApplyLogLevelFlag(fs *flag.FlagSet, logLevel string, opts *ctrlzap.Options) error {
	if !WasSet(fs, "log-level") {
		return nil
	}

	levelText := strings.TrimSpace(logLevel)
	var level zapcore.Level
	if err := level.Set(levelText); err != nil {
		return fmt.Errorf("invalid log-level %q: %w", logLevel, err)
	}

	opts.Level = level
	if !WasSet(fs, "zap-devel") {
		opts.Development = strings.EqualFold(levelText, "debug")
	}
	return nil
}
