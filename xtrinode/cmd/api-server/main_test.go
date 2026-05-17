package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestParseAPIServerOptions_LogLevelDebugConfiguresZap(t *testing.T) {
	options, zapOptions, err := parseAPIServerOptions([]string{"--log-level=debug"}, io.Discard)

	require.NoError(t, err)
	require.Equal(t, "debug", options.logLevel)
	require.True(t, zapOptions.Development)
	require.NotNil(t, zapOptions.Level)
	require.True(t, zapOptions.Level.Enabled(zapcore.DebugLevel))
}

func TestParseAPIServerOptions_ZapLogLevelPreservedWhenProjectLogLevelUnset(t *testing.T) {
	_, zapOptions, err := parseAPIServerOptions([]string{"--zap-log-level=error"}, io.Discard)

	require.NoError(t, err)
	require.NotNil(t, zapOptions.Level)
	require.True(t, zapOptions.Level.Enabled(zapcore.ErrorLevel))
	require.False(t, zapOptions.Level.Enabled(zapcore.WarnLevel))
}

func TestParseAPIServerOptions_InvalidLogLevel(t *testing.T) {
	_, _, err := parseAPIServerOptions([]string{"--log-level=verbose"}, io.Discard)

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid log-level")
}

func TestParseAPIServerOptions_ParsesRuntimeConfig(t *testing.T) {
	options, _, err := parseAPIServerOptions([]string{
		"--api-port=9090",
		"--api-path=/control/v1",
		"--health-path=/ready",
		"--metrics-path=/prometheus",
		"--read-timeout=11s",
		"--write-timeout=22s",
		"--shutdown-timeout=3s",
		"--resume-lease-duration=45s",
		"--lease-namespace=leases",
		"--lease-holder-identity=holder-a",
		"--auth-enabled",
		"--auth-token-file=/tmp/admin-token",
		"--resume-auth-token-file=/tmp/resume-token",
		"--cors-allowed-origins=https://one.example, https://two.example",
	}, io.Discard)

	require.NoError(t, err)
	require.Equal(t, 9090, options.apiPort)
	require.Equal(t, "/control/v1", options.apiPath)
	require.Equal(t, "/ready", options.healthPath)
	require.Equal(t, "/prometheus", options.metricsPath)
	require.Equal(t, "11s", options.readTimeoutStr)
	require.Equal(t, "22s", options.writeTimeoutStr)
	require.Equal(t, "3s", options.shutdownTimeoutStr)
	require.Equal(t, "45s", options.resumeLeaseDurationStr)
	require.Equal(t, "leases", options.leaseNamespace)
	require.Equal(t, "holder-a", options.leaseHolderIdentity)
	require.True(t, options.authEnabled)
	require.Equal(t, "/tmp/admin-token", options.authTokenFile)
	require.Equal(t, "/tmp/resume-token", options.resumeAuthTokenFile)
	require.Equal(t, "https://one.example, https://two.example", options.corsAllowedOrigins)
}

func TestLoadOptionalBearerToken_AllowsMissingOptionalToken(t *testing.T) {
	t.Setenv("XTRINODE_TEST_OPTIONAL_TOKEN", "")

	token, err := loadOptionalBearerToken(true, "", "XTRINODE_TEST_OPTIONAL_TOKEN")

	require.NoError(t, err)
	require.Empty(t, token)
}

func TestLoadOptionalBearerToken_RejectsEmptyConfiguredFile(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte(" \n"), 0o600))

	token, err := loadOptionalBearerToken(true, tokenFile, "XTRINODE_TEST_OPTIONAL_TOKEN")

	require.Error(t, err)
	require.Empty(t, token)
	require.Contains(t, err.Error(), "is empty")
}

func TestValidateAuthTokenConfiguration_RejectsSharedAdminAndResumeToken(t *testing.T) {
	err := validateAuthTokenConfiguration(true, "same-token", "same-token")

	require.Error(t, err)
	require.Contains(t, err.Error(), "must differ")
}
