package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/sizing"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestBuildConfigMaps_FaultTolerantExecutionDefaultsToTaskRetryAndDefaultExchange(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			FaultTolerantExecution: &analyticsv1.FaultTolerantExecutionSpec{
				ExchangeManager: &analyticsv1.ExchangeManagerSpec{},
			},
		},
	}

	coordinator, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)
	worker, err := BuildWorkerConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)

	for _, configMap := range []string{
		coordinator.Data["config.properties"],
		worker.Data["config.properties"],
	} {
		assert.Contains(t, configMap, "retry-policy=TASK")
	}
	for _, exchangeManager := range []string{
		coordinator.Data["exchange-manager.properties"],
		worker.Data["exchange-manager.properties"],
	} {
		assert.Contains(t, exchangeManager, "exchange-manager.name=filesystem")
		assert.Contains(t, exchangeManager, "exchange.base-directories=/tmp/trino-exchange")
	}
}

func TestBuildConfigMaps_FaultTolerantExecutionConfiguresRetryAndExchange(t *testing.T) {
	preset := sizing.Presets["s"]
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			FaultTolerantExecution: &analyticsv1.FaultTolerantExecutionSpec{
				RetryPolicy: "query",
				ExchangeManager: &analyticsv1.ExchangeManagerSpec{
					Name:            "filesystem",
					BaseDirectories: []string{"s3://trino-exchange/runtime-a", "s3://trino-exchange/runtime-b"},
					Properties: map[string]string{
						"exchange.s3.iam-role": "arn:aws:iam::123456789012:role/trino-exchange",
						"exchange.s3.region":   "us-east-1",
					},
				},
			},
		},
	}

	coordinator, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)
	worker, err := BuildWorkerConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)

	for _, configMap := range []string{
		coordinator.Data["config.properties"],
		worker.Data["config.properties"],
	} {
		assert.Contains(t, configMap, "retry-policy=QUERY")
	}
	for _, exchangeManager := range []string{
		coordinator.Data["exchange-manager.properties"],
		worker.Data["exchange-manager.properties"],
	} {
		assert.Contains(t, exchangeManager, "exchange-manager.name=filesystem")
		assert.Contains(t, exchangeManager, "exchange.base-directories=s3://trino-exchange/runtime-a,s3://trino-exchange/runtime-b")
		assert.Contains(t, exchangeManager, "exchange.s3.iam-role=arn:aws:iam::123456789012:role/trino-exchange")
		assert.Contains(t, exchangeManager, "exchange.s3.region=us-east-1")
		assert.NotContains(t, exchangeManager, "exchange.base-directories=/tmp/trino-exchange")
	}
}

func TestBuildConfigMaps_QueryRetryCanDisableExchangeManager(t *testing.T) {
	preset := sizing.Presets["s"]
	disabled := false
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-trino",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			FaultTolerantExecution: &analyticsv1.FaultTolerantExecutionSpec{
				RetryPolicy: "QUERY",
				ExchangeManager: &analyticsv1.ExchangeManagerSpec{
					Enabled: &disabled,
				},
			},
		},
	}

	coordinator, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)
	worker, err := BuildWorkerConfigMap(xtrinode, &preset, nil, "rev")
	require.NoError(t, err)

	assert.Contains(t, coordinator.Data["config.properties"], "retry-policy=QUERY")
	assert.Contains(t, worker.Data["config.properties"], "retry-policy=QUERY")
	assert.NotContains(t, coordinator.Data, "exchange-manager.properties")
	assert.NotContains(t, worker.Data, "exchange-manager.properties")
}
