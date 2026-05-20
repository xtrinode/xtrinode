package main

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xtrinode/xtrinode/internal/config"
)

func TestParseOperatorOptions_ParsesRuntimeConfig(t *testing.T) {
	options, _, err := parseOperatorOptions([]string{
		"--leader-elect=false",
		"--max-concurrent-reconciles=7",
		"--max-concurrent-reconciles-catalog=4",
		"--gateway-drain-duration=7m",
		"--gateway-drain-requeue-interval=11s",
		"--webhook-enabled=false",
		"--webhook-port=9444",
		"--webhook-cert-dir=/tmp/webhook",
	}, io.Discard)

	require.NoError(t, err)
	require.False(t, options.enableLeaderElection)
	require.Equal(t, 7, options.maxConcurrentReconciles)
	require.Equal(t, 4, options.maxConcurrentReconcilesCatalog)
	require.Equal(t, 7*time.Minute, options.gatewayDrainDuration)
	require.Equal(t, 11*time.Second, options.gatewayDrainRequeueInterval)
	require.False(t, options.webhookEnabled)
	require.Equal(t, 9444, options.webhookPort)
	require.Equal(t, "/tmp/webhook", options.webhookCertDir)
}

func TestParseOperatorOptions_ZapFlags(t *testing.T) {
	_, zapOptions, err := parseOperatorOptions([]string{"--zap-devel=false"}, io.Discard)

	require.NoError(t, err)
	require.False(t, zapOptions.Development)
}

func TestOperatorNamespaceFromEnv(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "operators")

	require.Equal(t, "operators", operatorNamespaceFromEnv())
}

func TestOperatorNamespaceFromEnv_Default(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "")

	require.Equal(t, config.OperatorDefaultNamespace, operatorNamespaceFromEnv())
}

func TestBuildManagerOptions_WebhookToggle(t *testing.T) {
	options := defaultOperatorOptions()
	options.webhookEnabled = false

	managerOptions := buildManagerOptions(options, "operators")

	require.Equal(t, scheme, managerOptions.Scheme)
	require.Equal(t, "operators", managerOptions.LeaderElectionNamespace)
	require.Nil(t, managerOptions.WebhookServer)

	options.webhookEnabled = true
	options.webhookPort = 9444
	options.webhookCertDir = "/tmp/webhook"

	managerOptions = buildManagerOptions(options, "operators")

	require.NotNil(t, managerOptions.WebhookServer)
}
