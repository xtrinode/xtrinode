package resources

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPruneDisabledTrinoResourcesDeletesOwnedWorkerStack(t *testing.T) {
	ctx := context.Background()
	xtrinode := pruneXTrinode()

	ownedWorkerConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "trino-runtime-worker-oldrev",
			Namespace:       xtrinode.Namespace,
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
	}
	unownedWorkerConfig := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "trino-runtime-worker-unowned",
			Namespace: xtrinode.Namespace,
		},
	}

	cli := pruneClient(t,
		pruneDeployment(xtrinode, workerDeploymentName(xtrinode)),
		pruneService(xtrinode, workerServiceName(xtrinode)),
		pruneService(xtrinode, workerMetricsServiceName(xtrinode)),
		prunePDB(xtrinode, workerPDBName(xtrinode)),
		ownedWorkerConfig,
		unownedWorkerConfig,
	)

	require.NoError(t, pruneDisabledTrinoResources(ctx, cli, xtrinode, &TrinoResourceSet{}))

	requireNotFound(t, cli, pruneDeployment(xtrinode, workerDeploymentName(xtrinode)))
	requireNotFound(t, cli, pruneService(xtrinode, workerServiceName(xtrinode)))
	requireNotFound(t, cli, pruneService(xtrinode, workerMetricsServiceName(xtrinode)))
	requireNotFound(t, cli, prunePDB(xtrinode, workerPDBName(xtrinode)))
	requireNotFound(t, cli, ownedWorkerConfig)
	requireStillExists(t, cli, unownedWorkerConfig)
}

func TestPruneDisabledTrinoResourcesDeletesToggledOptionalResources(t *testing.T) {
	ctx := context.Background()
	xtrinode := pruneXTrinode()
	cli := pruneClient(t,
		pruneIngress(xtrinode, ingressName(xtrinode)),
		pruneNetworkPolicy(xtrinode, config.BuildCoordinatorServiceName(xtrinode.Name)),
		pruneHPA(xtrinode, config.BuildWorkerServiceName(xtrinode.Name)),
		pruneConfigMap(xtrinode, "trino-runtime-session-property-config"),
	)

	resources := &TrinoResourceSet{
		WorkerDeployment: pruneDeployment(xtrinode, workerDeploymentName(xtrinode)),
		WorkerService:    pruneService(xtrinode, workerServiceName(xtrinode)),
	}

	require.NoError(t, pruneDisabledTrinoResources(ctx, cli, xtrinode, resources))

	requireNotFound(t, cli, pruneIngress(xtrinode, ingressName(xtrinode)))
	requireNotFound(t, cli, pruneNetworkPolicy(xtrinode, config.BuildCoordinatorServiceName(xtrinode.Name)))
	requireNotFound(t, cli, pruneHPA(xtrinode, config.BuildWorkerServiceName(xtrinode.Name)))
	requireNotFound(t, cli, pruneConfigMap(xtrinode, "trino-runtime-session-property-config"))
}

func pruneXTrinode() *analyticsv1.XTrinode {
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "team-a",
			UID:       types.UID("runtime-uid"),
		},
		Spec: analyticsv1.XTrinodeSpec{Size: "s"},
	}
}

func pruneClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, policyv1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, autoscalingv2.AddToScheme(scheme))
	require.NoError(t, apiextensionsv1.AddToScheme(scheme))
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func pruneDeployment(xtrinode *analyticsv1.XTrinode, name string) *appsv1.Deployment {
	return &appsv1.Deployment{ObjectMeta: pruneObjectMeta(xtrinode, name)}
}

func pruneService(xtrinode *analyticsv1.XTrinode, name string) *corev1.Service {
	return &corev1.Service{ObjectMeta: pruneObjectMeta(xtrinode, name)}
}

func prunePDB(xtrinode *analyticsv1.XTrinode, name string) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{ObjectMeta: pruneObjectMeta(xtrinode, name)}
}

func pruneIngress(xtrinode *analyticsv1.XTrinode, name string) *networkingv1.Ingress {
	return &networkingv1.Ingress{ObjectMeta: pruneObjectMeta(xtrinode, name)}
}

func pruneNetworkPolicy(xtrinode *analyticsv1.XTrinode, name string) *networkingv1.NetworkPolicy {
	return &networkingv1.NetworkPolicy{ObjectMeta: pruneObjectMeta(xtrinode, name)}
}

func pruneHPA(xtrinode *analyticsv1.XTrinode, name string) *autoscalingv2.HorizontalPodAutoscaler {
	return &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: pruneObjectMeta(xtrinode, name)}
}

func pruneConfigMap(xtrinode *analyticsv1.XTrinode, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{ObjectMeta: pruneObjectMeta(xtrinode, name)}
}

func pruneObjectMeta(xtrinode *analyticsv1.XTrinode, name string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:            name,
		Namespace:       xtrinode.Namespace,
		OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
	}
}

func requireNotFound(t *testing.T, cli client.Client, obj client.Object) {
	t.Helper()
	require.True(t, apierrors.IsNotFound(cli.Get(context.Background(), client.ObjectKeyFromObject(obj), obj)))
}

func requireStillExists(t *testing.T, cli client.Client, obj client.Object) {
	t.Helper()
	require.NoError(t, cli.Get(context.Background(), client.ObjectKeyFromObject(obj), obj))
}
