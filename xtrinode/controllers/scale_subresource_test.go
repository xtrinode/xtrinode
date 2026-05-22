package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

func TestScaleDeploymentViaSubresourceUpdatesReplicas(t *testing.T) {
	scheme := newTestScheme()
	initialReplicas := int32(1)
	deployment := testScaleDeployment("runtime-coordinator", "default", initialReplicas)
	cli := newTestClient(scheme, deployment)
	reconciler := newTestReconciler(cli, scheme)

	desiredReplicas := int32(4)
	err := reconciler.scaleDeploymentViaSubresource(context.Background(), deployment, desiredReplicas, newTestLogger())
	require.NoError(t, err)

	updated := &appsv1.Deployment{}
	err = cli.Get(context.Background(), types.NamespacedName{
		Name:      deployment.Name,
		Namespace: deployment.Namespace,
	}, updated)
	require.NoError(t, err)
	require.NotNil(t, updated.Spec.Replicas)
	assert.Equal(t, desiredReplicas, *updated.Spec.Replicas)
}

func TestScaleDeploymentIgnoresMissingDeployment(t *testing.T) {
	scheme := newTestScheme()
	reconciler := newTestReconciler(newTestClient(scheme), scheme)
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "default",
		},
	}

	err := reconciler.scaleDeployment(context.Background(), xtrinode, "missing-worker", 0, "worker", newTestLogger())
	require.NoError(t, err)
}

func TestScaleForResumeSeedsWorkersWhenNativeHPAEnabledAndTargetIsZero(t *testing.T) {
	scheme := newTestScheme()
	workerReplicas := int32(0)
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"server": map[string]interface{}{
					"workers": int64(0),
					"autoscaling": map[string]interface{}{
						"enabled":                           true,
						"minReplicas":                       int64(2),
						"maxReplicas":                       int64(4),
						"targetCPUUtilizationPercentage":    int64(70),
						"targetMemoryUtilizationPercentage": "",
					},
				},
			}),
		},
	}
	coordinator := testScaleDeployment(config.BuildCoordinatorDeploymentName(xtrinode.Name), xtrinode.Namespace, 0)
	worker := testScaleDeployment(config.BuildWorkerDeploymentName(xtrinode.Name), xtrinode.Namespace, workerReplicas)
	cli := newTestClient(scheme, xtrinode, coordinator, worker)
	reconciler := newTestReconciler(cli, scheme)

	err := reconciler.scaleForResume(context.Background(), xtrinode, 3)
	require.NoError(t, err)

	updatedCoordinator := &appsv1.Deployment{}
	require.NoError(t, cli.Get(context.Background(), types.NamespacedName{Name: coordinator.Name, Namespace: coordinator.Namespace}, updatedCoordinator))
	require.NotNil(t, updatedCoordinator.Spec.Replicas)
	assert.Equal(t, int32(1), *updatedCoordinator.Spec.Replicas)

	updatedWorker := &appsv1.Deployment{}
	require.NoError(t, cli.Get(context.Background(), types.NamespacedName{Name: worker.Name, Namespace: worker.Namespace}, updatedWorker))
	require.NotNil(t, updatedWorker.Spec.Replicas)
	assert.Equal(t, int32(2), *updatedWorker.Spec.Replicas)
}

func TestScaleForResumeDoesNotScaleWorkersWhenKEDAEnabled(t *testing.T) {
	scheme := newTestScheme()
	workerReplicas := int32(0)
	enabled := true
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			KEDA: &analyticsv1.KEDASpec{
				Enabled:       &enabled,
				ScalerType:    "prometheus",
				ScalingMetric: "query",
			},
		},
	}
	coordinator := testScaleDeployment(config.BuildCoordinatorDeploymentName(xtrinode.Name), xtrinode.Namespace, 0)
	worker := testScaleDeployment(config.BuildWorkerDeploymentName(xtrinode.Name), xtrinode.Namespace, workerReplicas)
	cli := newTestClient(scheme, xtrinode, coordinator, worker)
	reconciler := newTestReconciler(cli, scheme)

	err := reconciler.scaleForResume(context.Background(), xtrinode, 3)
	require.NoError(t, err)

	updatedCoordinator := &appsv1.Deployment{}
	require.NoError(t, cli.Get(context.Background(), types.NamespacedName{Name: coordinator.Name, Namespace: coordinator.Namespace}, updatedCoordinator))
	require.NotNil(t, updatedCoordinator.Spec.Replicas)
	assert.Equal(t, int32(1), *updatedCoordinator.Spec.Replicas)

	updatedWorker := &appsv1.Deployment{}
	require.NoError(t, cli.Get(context.Background(), types.NamespacedName{Name: worker.Name, Namespace: worker.Namespace}, updatedWorker))
	require.NotNil(t, updatedWorker.Spec.Replicas)
	assert.Equal(t, workerReplicas, *updatedWorker.Spec.Replicas)
}

func TestEnsureResumedInvariantsSeedsNativeHPAWorkerFloor(t *testing.T) {
	scheme := newTestScheme()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"server": map[string]interface{}{
					"workers": int64(0),
					"autoscaling": map[string]interface{}{
						"enabled":                           true,
						"minReplicas":                       int64(2),
						"maxReplicas":                       int64(4),
						"targetCPUUtilizationPercentage":    int64(70),
						"targetMemoryUtilizationPercentage": "",
					},
				},
			}),
		},
	}
	coordinator := testScaleDeployment(config.BuildCoordinatorDeploymentName(xtrinode.Name), xtrinode.Namespace, 1)
	worker := testScaleDeployment(config.BuildWorkerDeploymentName(xtrinode.Name), xtrinode.Namespace, 0)
	cli := newTestClient(scheme, xtrinode, coordinator, worker)
	reconciler := newTestReconciler(cli, scheme)

	err := reconciler.ensureResumedInvariants(context.Background(), xtrinode)
	require.NoError(t, err)

	updatedWorker := &appsv1.Deployment{}
	require.NoError(t, cli.Get(context.Background(), types.NamespacedName{Name: worker.Name, Namespace: worker.Namespace}, updatedWorker))
	require.NotNil(t, updatedWorker.Spec.Replicas)
	assert.Equal(t, int32(2), *updatedWorker.Spec.Replicas)
}

func TestEnsureResumedInvariantsDoesNotLowerNativeHPAWorkerScale(t *testing.T) {
	scheme := newTestScheme()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
			ValuesOverlay: controllerValuesOverlay(t, map[string]interface{}{
				"server": map[string]interface{}{
					"workers": int64(0),
					"autoscaling": map[string]interface{}{
						"enabled":                           true,
						"minReplicas":                       int64(2),
						"maxReplicas":                       int64(4),
						"targetCPUUtilizationPercentage":    int64(70),
						"targetMemoryUtilizationPercentage": "",
					},
				},
			}),
		},
	}
	coordinator := testScaleDeployment(config.BuildCoordinatorDeploymentName(xtrinode.Name), xtrinode.Namespace, 1)
	worker := testScaleDeployment(config.BuildWorkerDeploymentName(xtrinode.Name), xtrinode.Namespace, 3)
	cli := newTestClient(scheme, xtrinode, coordinator, worker)
	reconciler := newTestReconciler(cli, scheme)

	err := reconciler.ensureResumedInvariants(context.Background(), xtrinode)
	require.NoError(t, err)

	updatedWorker := &appsv1.Deployment{}
	require.NoError(t, cli.Get(context.Background(), types.NamespacedName{Name: worker.Name, Namespace: worker.Namespace}, updatedWorker))
	require.NotNil(t, updatedWorker.Spec.Replicas)
	assert.Equal(t, int32(3), *updatedWorker.Spec.Replicas)
}

func testScaleDeployment(name, namespace string, replicas int32) *appsv1.Deployment {
	labels := map[string]string{"app": name}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "trino",
							Image: "trinodb/trino:480",
						},
					},
				},
			},
		},
	}
}
