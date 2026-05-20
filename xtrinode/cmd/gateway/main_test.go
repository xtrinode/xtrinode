package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

func TestParseGatewayOptions_LogLevelDebugConfiguresZap(t *testing.T) {
	options, zapOptions, err := parseGatewayOptions([]string{"--log-level=debug"}, io.Discard)

	require.NoError(t, err)
	require.Equal(t, "debug", options.logLevel)
	require.True(t, zapOptions.Development)
	require.NotNil(t, zapOptions.Level)
	require.True(t, zapOptions.Level.Enabled(zapcore.DebugLevel))
}

func TestParseGatewayOptions_ZapLogLevelPreservedWhenProjectLogLevelUnset(t *testing.T) {
	_, zapOptions, err := parseGatewayOptions([]string{"--zap-log-level=error"}, io.Discard)

	require.NoError(t, err)
	require.NotNil(t, zapOptions.Level)
	require.True(t, zapOptions.Level.Enabled(zapcore.ErrorLevel))
	require.False(t, zapOptions.Level.Enabled(zapcore.WarnLevel))
}

func TestParseGatewayOptions_InvalidLogLevel(t *testing.T) {
	_, _, err := parseGatewayOptions([]string{"--log-level=verbose"}, io.Discard)

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid log-level")
}

func TestParseGatewayOptions_ParsesRuntimeConfig(t *testing.T) {
	options, _, err := parseGatewayOptions([]string{
		"--api-server-url=http://api.example/api/v1",
		"--gateway-port=9091",
		"--api-server-auth-token-file=/tmp/api-token",
		"--auth-enabled",
		"--auth-type=oidc",
		"--auth-secret-name=gateway-auth",
		"--auth-secret-key=keys.json",
		"--auth-namespace=auth-ns",
		"--auth-oauth-issuer=https://issuer.example",
		"--auth-oauth-audience=trino",
		"--auth-oauth-jwks-url=https://issuer.example/jwks",
		"--auth-oauth-refresh-interval=2h",
		"--redis-enabled",
		"--redis-url=redis://redis.example:6379/0",
		"--redis-password=secret",
		"--redis-db=2",
		"--redis-sticky-ttl=15m",
		"--redis-timeout=4s",
		"--rate-limit-enabled=false",
		"--rate-limit-capacity=17",
		"--rate-limit-refill-rate=250ms",
		"--read-header-timeout=6s",
		"--read-timeout=0s",
		"--write-timeout=0s",
		"--idle-timeout=90s",
		"--ui-enabled",
		"--ui-require-auth=false",
	}, io.Discard)

	require.NoError(t, err)
	require.Equal(t, "http://api.example/api/v1", options.apiServerURL)
	require.Equal(t, 9091, options.gatewayPort)
	require.Equal(t, "/tmp/api-token", options.apiServerAuthTokenFile)
	require.True(t, options.authEnabled)
	require.Equal(t, "oidc", options.authType)
	require.Equal(t, "gateway-auth", options.authSecretName)
	require.Equal(t, "keys.json", options.authSecretKey)
	require.Equal(t, "auth-ns", options.authNamespace)
	require.Equal(t, "https://issuer.example", options.authOAuthIssuer)
	require.Equal(t, "trino", options.authOAuthAudience)
	require.Equal(t, "https://issuer.example/jwks", options.authOAuthJWKSURL)
	require.Equal(t, 2*time.Hour, options.authOAuthRefreshInterval)
	require.True(t, options.redisEnabled)
	require.Equal(t, "redis://redis.example:6379/0", options.redisURL)
	require.Equal(t, "secret", options.redisPassword)
	require.Equal(t, 2, options.redisDB)
	require.Equal(t, 15*time.Minute, options.redisStickyTTL)
	require.Equal(t, 4*time.Second, options.redisTimeout)
	require.False(t, options.rateLimitEnabled)
	require.Equal(t, 17, options.rateLimitCapacity)
	require.Equal(t, 250*time.Millisecond, options.rateLimitRefillRate)
	require.Equal(t, 6*time.Second, options.readHeaderTimeout)
	require.Zero(t, options.readTimeout)
	require.Zero(t, options.writeTimeout)
	require.Equal(t, 90*time.Second, options.idleTimeout)
	require.True(t, options.uiEnabled)
	require.False(t, options.uiRequireAuth)
}

func TestLoadBearerToken_UsesEnvironmentToken(t *testing.T) {
	t.Setenv("XTRINODE_TEST_API_TOKEN", " env-token \n")

	token, err := loadBearerToken("", "XTRINODE_TEST_API_TOKEN")

	require.NoError(t, err)
	require.Equal(t, "env-token", token)
}

func TestLoadBearerToken_FileOverridesEnvironmentToken(t *testing.T) {
	t.Setenv("XTRINODE_TEST_API_TOKEN", "env-token")
	tokenFile := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte(" file-token \n"), 0o600))

	token, err := loadBearerToken(tokenFile, "XTRINODE_TEST_API_TOKEN")

	require.NoError(t, err)
	require.Equal(t, "file-token", token)
}
