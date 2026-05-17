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
