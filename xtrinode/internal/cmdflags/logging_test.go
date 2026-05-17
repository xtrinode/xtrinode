package cmdflags

import (
	"flag"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	ctrlzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestApplyLogLevelFlag_ConfiguresExplicitProjectLogLevel(t *testing.T) {
	var logLevel string
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&logLevel, "log-level", "info", "")
	require.NoError(t, fs.Parse([]string{"--log-level=debug"}))

	zapOptions := ctrlzap.Options{}
	err := ApplyLogLevelFlag(fs, logLevel, &zapOptions)

	require.NoError(t, err)
	require.True(t, zapOptions.Development)
	require.NotNil(t, zapOptions.Level)
	require.True(t, zapOptions.Level.Enabled(zapcore.DebugLevel))
}

func TestApplyLogLevelFlag_DoesNotOverrideUnsetProjectLogLevel(t *testing.T) {
	var logLevel string
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&logLevel, "log-level", "info", "")
	zapOptions := ctrlzap.Options{Development: true}
	require.NoError(t, fs.Parse(nil))

	err := ApplyLogLevelFlag(fs, logLevel, &zapOptions)

	require.NoError(t, err)
	require.True(t, zapOptions.Development)
	require.Nil(t, zapOptions.Level)
}

func TestApplyLogLevelFlag_PreservesExplicitZapDevelopmentFlag(t *testing.T) {
	var logLevel string
	zapOptions := ctrlzap.Options{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&logLevel, "log-level", "info", "")
	fs.BoolVar(&zapOptions.Development, "zap-devel", false, "")
	require.NoError(t, fs.Parse([]string{"--log-level=debug", "--zap-devel=false"}))

	err := ApplyLogLevelFlag(fs, logLevel, &zapOptions)

	require.NoError(t, err)
	require.False(t, zapOptions.Development)
	require.NotNil(t, zapOptions.Level)
	require.True(t, zapOptions.Level.Enabled(zapcore.DebugLevel))
}
