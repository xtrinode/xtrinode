package resources

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/rollout"
	"github.com/xtrinode/xtrinode/internal/sizing"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestBuildTrinoResourceSetWithOptionalResources(t *testing.T) {
	ctx := context.Background()
	xtrinode := resourceCoverageXTrinode()
	cli := resourceCoverageClient(t,
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "trino-catalog-hive", Namespace: xtrinode.Namespace},
			Data:       map[string]string{"hive.properties": "connector.name=hive"},
		},
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "trino-catalog-tpch", Namespace: xtrinode.Namespace},
			Data:       map[string]string{"tpch.properties": "connector.name=tpch"},
		},
	)

	set, err := BuildTrinoResourceSet(ctx, cli, xtrinode, []string{"tpch", "hive"}, "test-version")
	require.NoError(t, err)
	require.NotNil(t, set)

	require.NotNil(t, set.CoordinatorDeployment)
	require.NotNil(t, set.WorkerDeployment)
	assert.NotEmpty(t, set.CoordinatorDeployment.Spec.Template.Annotations[rollout.CoordinatorRolloutHashKey])
	assert.NotEmpty(t, set.WorkerDeployment.Spec.Template.Annotations[rollout.WorkerRolloutHashKey])

	assert.NotNil(t, set.SessionPropertyConfigMap)
	assert.Contains(t, set.SessionPropertyConfigMap.Data, "session-property-config.json")
	assert.NotNil(t, set.KafkaSchemasConfigMapCoord)
	assert.Equal(t, "schema-json", set.KafkaSchemasConfigMapCoord.Data["orders.json"])
	assert.NotNil(t, set.KafkaSchemasConfigMapWorker)

	assert.NotNil(t, set.PasswordAuthSecret)
	assert.NotNil(t, set.GroupsAuthSecret)
	assert.NotNil(t, set.AccessControlConfigMapCoord)
	assert.NotNil(t, set.AccessControlConfigMapWorker)
	assert.NotNil(t, set.ResourceGroupsConfigMapCoord)
	assert.Nil(t, set.ResourceGroupsConfigMapWorker, "resource groups should remain coordinator-only")

	assert.NotNil(t, set.NetworkPolicy)
	assert.NotNil(t, set.HorizontalPodAutoscaler)
	assert.NotNil(t, set.CoordinatorServiceMonitor)
	assert.NotNil(t, set.WorkerServiceMonitor)
	assert.NotNil(t, set.CoordinatorJMXExporterConfigMap)
	assert.NotNil(t, set.WorkerJMXExporterConfigMap)

	names := resourceNames(set.AllResources())
	assert.Contains(t, names, "trino-coverage")
	assert.Contains(t, names, "trino-coverage-worker")
	assert.Contains(t, names, "trino-coverage-session-property-config")
	assert.Contains(t, names, "trino-coverage-access-control-volume-coordinator")
	assert.Contains(t, names, "trino-coverage-access-control-volume-worker")
	assert.Contains(t, names, "trino-coverage-resource-groups-volume-coordinator")
}

func TestBuildTrinoResourceSetRollsBothRolesOnCatalogConfigMapChange(t *testing.T) {
	ctx := context.Background()
	xtrinode := resourceCoverageBaseXTrinode()

	buildHashes := func(catalogProperties string) (string, string) {
		t.Helper()
		cli := resourceCoverageClient(t, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "trino-catalog-hive", Namespace: xtrinode.Namespace},
			Data:       map[string]string{"hive.properties": catalogProperties},
		})

		set, err := BuildTrinoResourceSet(ctx, cli, xtrinode.DeepCopy(), []string{"hive"}, "test-version")
		require.NoError(t, err)
		require.NotNil(t, set.CoordinatorDeployment)
		require.NotNil(t, set.WorkerDeployment)
		return set.CoordinatorDeployment.Spec.Template.Annotations[rollout.CoordinatorRolloutHashKey],
			set.WorkerDeployment.Spec.Template.Annotations[rollout.WorkerRolloutHashKey]
	}

	firstCoord, firstWorker := buildHashes("connector.name=hive\nhive.metastore.uri=thrift://hive-a:9083")
	secondCoord, secondWorker := buildHashes("connector.name=hive\nhive.metastore.uri=thrift://hive-b:9083")

	require.NotEqual(t, firstCoord, secondCoord, "coordinator must roll on catalog ConfigMap data changes")
	require.NotEqual(t, firstWorker, secondWorker, "worker must roll on catalog ConfigMap data changes because catalogs are mounted there")
}

func TestBuildTrinoResourceSetDoesNotRollPodsForRoutingOnlyChange(t *testing.T) {
	ctx := context.Background()
	base := resourceCoverageBaseXTrinode()
	base.Spec.Routing = &analyticsv1.RoutingSpec{
		RoutingGroup: "dedicated",
	}
	routed := base.DeepCopy()
	routed.Spec.Routing = &analyticsv1.RoutingSpec{
		RoutingGroup:   "shared",
		HostnameDomain: "trino.example.com",
		Header:         "X-Trino-XTrinode=coverage",
	}

	buildSet := func(xtrinode *analyticsv1.XTrinode) *TrinoResourceSet {
		t.Helper()
		set, err := BuildTrinoResourceSet(ctx, resourceCoverageClient(t), xtrinode, nil, "test-version")
		require.NoError(t, err)
		require.NotNil(t, set.CoordinatorDeployment)
		require.NotNil(t, set.WorkerDeployment)
		require.NotNil(t, set.CoordinatorConfigMap)
		require.NotNil(t, set.WorkerConfigMap)
		return set
	}

	first := buildSet(base)
	second := buildSet(routed)

	assert.NotEqual(
		t,
		first.CoordinatorDeployment.Annotations[config.RevisionAnnotationKey],
		second.CoordinatorDeployment.Annotations[config.RevisionAnnotationKey],
		"resource revision should still track the full CR spec for convergence/debugging",
	)
	assert.Equal(t, first.CoordinatorConfigMap.Name, second.CoordinatorConfigMap.Name)
	assert.Equal(t, first.WorkerConfigMap.Name, second.WorkerConfigMap.Name)
	assert.Equal(
		t,
		first.CoordinatorDeployment.Spec.Template.Annotations[config.RevisionAnnotationKey],
		second.CoordinatorDeployment.Spec.Template.Annotations[config.RevisionAnnotationKey],
	)
	assert.Equal(
		t,
		first.WorkerDeployment.Spec.Template.Annotations[config.RevisionAnnotationKey],
		second.WorkerDeployment.Spec.Template.Annotations[config.RevisionAnnotationKey],
	)
	assert.Equal(
		t,
		first.CoordinatorDeployment.Spec.Template.Annotations[rollout.CoordinatorRolloutHashKey],
		second.CoordinatorDeployment.Spec.Template.Annotations[rollout.CoordinatorRolloutHashKey],
	)
	assert.Equal(
		t,
		first.WorkerDeployment.Spec.Template.Annotations[rollout.WorkerRolloutHashKey],
		second.WorkerDeployment.Spec.Template.Annotations[rollout.WorkerRolloutHashKey],
	)
}

func TestBuildTrinoResourceSetDoesNotRollPodsForBaseRevisionOnlyChange(t *testing.T) {
	ctx := context.Background()
	xtrinode := resourceCoverageBaseXTrinode()

	buildSet := func(operatorVersion string) *TrinoResourceSet {
		t.Helper()
		set, err := BuildTrinoResourceSet(ctx, resourceCoverageClient(t), xtrinode.DeepCopy(), nil, operatorVersion)
		require.NoError(t, err)
		require.NotNil(t, set.CoordinatorDeployment)
		require.NotNil(t, set.WorkerDeployment)
		require.NotNil(t, set.CoordinatorConfigMap)
		require.NotNil(t, set.WorkerConfigMap)
		return set
	}

	first := buildSet("operator-a")
	second := buildSet("operator-b")

	assert.NotEqual(
		t,
		first.CoordinatorDeployment.Annotations[config.RevisionAnnotationKey],
		second.CoordinatorDeployment.Annotations[config.RevisionAnnotationKey],
		"resource metadata should keep tracking broad base revision changes",
	)
	assert.Equal(t, first.CoordinatorConfigMap.Name, second.CoordinatorConfigMap.Name)
	assert.Equal(t, first.WorkerConfigMap.Name, second.WorkerConfigMap.Name)
	assert.Equal(
		t,
		first.CoordinatorDeployment.Spec.Template.Annotations[config.RevisionAnnotationKey],
		second.CoordinatorDeployment.Spec.Template.Annotations[config.RevisionAnnotationKey],
	)
	assert.Equal(
		t,
		first.WorkerDeployment.Spec.Template.Annotations[config.RevisionAnnotationKey],
		second.WorkerDeployment.Spec.Template.Annotations[config.RevisionAnnotationKey],
	)
	assert.Equal(
		t,
		first.CoordinatorDeployment.Spec.Template.Annotations[rollout.CoordinatorRolloutHashKey],
		second.CoordinatorDeployment.Spec.Template.Annotations[rollout.CoordinatorRolloutHashKey],
	)
	assert.Equal(
		t,
		first.WorkerDeployment.Spec.Template.Annotations[rollout.WorkerRolloutHashKey],
		second.WorkerDeployment.Spec.Template.Annotations[rollout.WorkerRolloutHashKey],
	)
}

func TestPodRolloutHashIgnoresSelfAnnotations(t *testing.T) {
	template := &corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"checksum/config":                 "config-a",
				config.RevisionAnnotationKey:      "revision-a",
				rollout.CoordinatorRolloutHashKey: "coordinator-a",
				rollout.WorkerRolloutHashKey:      "worker-a",
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  "trino",
				Image: "trinodb/trino:480",
			}},
		},
	}
	digests := roleRolloutDigests{
		Catalog:       "catalog-a",
		AccessControl: "access-a",
		SessionProps:  "session-a",
		Secret:        "secret-a",
	}

	first := coordinatorPodRolloutHash(template, digests)
	template.Annotations[config.RevisionAnnotationKey] = "revision-b"
	template.Annotations[rollout.CoordinatorRolloutHashKey] = "coordinator-b"
	template.Annotations[rollout.WorkerRolloutHashKey] = "worker-b"
	second := coordinatorPodRolloutHash(template, digests)

	require.Equal(t, first, second, "rollout hash must ignore annotations it owns")

	template.Annotations["checksum/config"] = "config-b"
	third := coordinatorPodRolloutHash(template, digests)
	require.NotEqual(t, first, third, "rollout hash must still include rendered pod-template inputs")
}

func TestBuildTrinoResourceSetRollsPodsForRenderedTemplateChange(t *testing.T) {
	ctx := context.Background()
	base := resourceCoverageBaseXTrinode()
	changed := base.DeepCopy()
	changed.Spec.ValuesOverlay = mustValuesOverlay(map[string]interface{}{
		"image": map[string]interface{}{
			"repository": "trinodb/trino",
			"tag":        "481",
			"pullPolicy": "IfNotPresent",
		},
	})

	buildSet := func(xtrinode *analyticsv1.XTrinode) *TrinoResourceSet {
		t.Helper()
		set, err := BuildTrinoResourceSet(ctx, resourceCoverageClient(t), xtrinode, nil, "test-version")
		require.NoError(t, err)
		require.NotNil(t, set.CoordinatorDeployment)
		require.NotNil(t, set.WorkerDeployment)
		return set
	}

	first := buildSet(base)
	second := buildSet(changed)

	assert.NotEqual(
		t,
		first.CoordinatorDeployment.Spec.Template.Annotations[rollout.CoordinatorRolloutHashKey],
		second.CoordinatorDeployment.Spec.Template.Annotations[rollout.CoordinatorRolloutHashKey],
	)
	assert.NotEqual(
		t,
		first.WorkerDeployment.Spec.Template.Annotations[rollout.WorkerRolloutHashKey],
		second.WorkerDeployment.Spec.Template.Annotations[rollout.WorkerRolloutHashKey],
	)
}

func TestBuildDeploymentsStampPodTemplateRevisionFromRolloutHash(t *testing.T) {
	xtrinode := resourceCoverageBaseXTrinode()
	preset := sizing.Presets["s"]

	coordinator, err := BuildCoordinatorDeployment(xtrinode, &preset, "test-config", nil, "base-revision", "coordinator-rollout", nil)
	require.NoError(t, err)
	worker, err := BuildWorkerDeployment(xtrinode, &preset, "test-config", nil, "base-revision", "worker-rollout", nil)
	require.NoError(t, err)

	assert.Equal(t, "base-revision", coordinator.Annotations[config.RevisionAnnotationKey])
	assert.Equal(t, "coordinator-rollout", coordinator.Spec.Template.Annotations[config.RevisionAnnotationKey])
	assert.Equal(t, "coordinator-rollout", coordinator.Spec.Template.Annotations[rollout.CoordinatorRolloutHashKey])
	assert.Equal(t, "base-revision", worker.Annotations[config.RevisionAnnotationKey])
	assert.Equal(t, "worker-rollout", worker.Spec.Template.Annotations[config.RevisionAnnotationKey])
	assert.Equal(t, "worker-rollout", worker.Spec.Template.Annotations[rollout.WorkerRolloutHashKey])
}

func TestBuildTrinoResourceSetSkipsWorkersWhenFixedWorkerCountIsZero(t *testing.T) {
	xtrinode := resourceCoverageBaseXTrinode()
	disabled := false
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{Enabled: &disabled}
	xtrinode.Spec.ValuesOverlay = mustValuesOverlay(map[string]interface{}{
		"server": map[string]interface{}{
			"workers": int64(0),
		},
	})

	set, err := BuildTrinoResourceSet(context.Background(), resourceCoverageClient(t), xtrinode, nil, "test-version")
	require.NoError(t, err)
	require.NotNil(t, set.CoordinatorDeployment)
	assert.Nil(t, set.WorkerDeployment)
	assert.Nil(t, set.WorkerConfigMap)
	assert.Nil(t, set.WorkerService)
	assert.Nil(t, set.WorkerMetricsService)
	assert.Nil(t, set.WorkerPDB)
}

func TestBuildTrinoResourceSetKeepsWorkersWhenNativeHPAEnabledWithZeroWorkers(t *testing.T) {
	xtrinode := resourceCoverageBaseXTrinode()
	xtrinode.Spec.ValuesOverlay = mustValuesOverlay(map[string]interface{}{
		"server": map[string]interface{}{
			"workers": int64(0),
			"autoscaling": map[string]interface{}{
				"enabled":                           true,
				"minReplicas":                       int64(1),
				"maxReplicas":                       int64(4),
				"targetCPUUtilizationPercentage":    int64(70),
				"targetMemoryUtilizationPercentage": "",
			},
		},
	})

	set, err := BuildTrinoResourceSet(context.Background(), resourceCoverageClient(t), xtrinode, nil, "test-version")
	require.NoError(t, err)
	require.NotNil(t, set.WorkerDeployment)
	assert.Nil(t, set.WorkerDeployment.Spec.Replicas, "native HPA owns worker replicas")
	require.NotNil(t, set.HorizontalPodAutoscaler)
	assert.Equal(t, int32(1), *set.HorizontalPodAutoscaler.Spec.MinReplicas)
}

func TestBuildTrinoResourceSetKeepsFixedWorkersForPositiveMinWorkers(t *testing.T) {
	xtrinode := resourceCoverageBaseXTrinode()
	disabled := false
	minWorkers := int32(2)
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{Enabled: &disabled}
	xtrinode.Spec.MinWorkers = &minWorkers
	xtrinode.Spec.ValuesOverlay = mustValuesOverlay(map[string]interface{}{
		"server": map[string]interface{}{
			"workers": int64(0),
		},
	})

	set, err := BuildTrinoResourceSet(context.Background(), resourceCoverageClient(t), xtrinode, nil, "test-version")
	require.NoError(t, err)
	require.NotNil(t, set.WorkerDeployment)
	require.NotNil(t, set.WorkerDeployment.Spec.Replicas)
	assert.Equal(t, minWorkers, *set.WorkerDeployment.Spec.Replicas)
}

func TestBuildWorkerDeploymentUsesMinWorkersAsFixedReplicaFloor(t *testing.T) {
	xtrinode := resourceCoverageBaseXTrinode()
	disabled := false
	minWorkers := int32(3)
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{Enabled: &disabled}
	xtrinode.Spec.MinWorkers = &minWorkers
	xtrinode.Spec.ValuesOverlay = mustValuesOverlay(map[string]interface{}{
		"server": map[string]interface{}{
			"workers": int64(1),
		},
	})

	set, err := BuildTrinoResourceSet(context.Background(), resourceCoverageClient(t), xtrinode, nil, "test-version")
	require.NoError(t, err)
	require.NotNil(t, set.WorkerDeployment)
	require.NotNil(t, set.WorkerDeployment.Spec.Replicas)
	assert.Equal(t, minWorkers, *set.WorkerDeployment.Spec.Replicas)
}

func TestDefaultTrinoImageIsPinnedToUpstreamAppVersion(t *testing.T) {
	xtrinode := resourceCoverageBaseXTrinode()

	set, err := BuildTrinoResourceSet(context.Background(), resourceCoverageClient(t), xtrinode, nil, "test-version")
	require.NoError(t, err)
	require.NotNil(t, set.CoordinatorDeployment)
	require.NotNil(t, set.CoordinatorConfigMap)

	container := set.CoordinatorDeployment.Spec.Template.Spec.Containers[0]
	assert.Equal(t, "trinodb/trino:480", container.Image)
	assert.Equal(t, "480", set.CoordinatorDeployment.Labels[AppVersionLabel])
	assert.Contains(t, set.CoordinatorConfigMap.Data["jvm.config"], "-XX:G1NumCollectionsKeepPinned=10000000")
}

func TestBuildTrinoResourceSetRejectsInvalidSize(t *testing.T) {
	xtrinode := resourceCoverageBaseXTrinode()
	xtrinode.Spec.Size = "not-a-size"

	set, err := BuildTrinoResourceSet(context.Background(), resourceCoverageClient(t), xtrinode, nil, "test-version")
	require.Error(t, err)
	assert.Nil(t, set)
	assert.Contains(t, err.Error(), "invalid size preset")
}

func TestDeleteTrinoResourcesDeletesExistingObjects(t *testing.T) {
	xtrinode := resourceCoverageBaseXTrinode()
	preset := sizing.Presets["s"]
	revision := "abc123"

	coordCM, err := BuildCoordinatorConfigMap(xtrinode, &preset, nil, revision)
	require.NoError(t, err)
	workerCM, err := BuildWorkerConfigMap(xtrinode, &preset, nil, revision)
	require.NoError(t, err)
	coordDeployment, err := BuildCoordinatorDeployment(xtrinode, &preset, coordCM.Name, nil, revision, "coordhash", nil)
	require.NoError(t, err)
	workerDeployment, err := BuildWorkerDeployment(xtrinode, &preset, workerCM.Name, nil, revision, "workerhash", nil)
	require.NoError(t, err)

	set := &TrinoResourceSet{
		CoordinatorDeployment:        coordDeployment,
		WorkerDeployment:             workerDeployment,
		CoordinatorService:           BuildCoordinatorService(xtrinode),
		WorkerService:                BuildWorkerService(xtrinode),
		CoordinatorMetricsService:    BuildCoordinatorMetricsService(xtrinode),
		WorkerMetricsService:         BuildWorkerMetricsService(xtrinode),
		CoordinatorConfigMap:         coordCM,
		WorkerConfigMap:              workerCM,
		CatalogConfigMap:             namedConfigMap(xtrinode, "trino-coverage-catalog"),
		ServiceAccount:               BuildServiceAccount(xtrinode),
		SessionPropertyConfigMap:     BuildSessionPropertyConfigMap(xtrinode),
		KafkaSchemasConfigMapCoord:   BuildKafkaSchemasConfigMap(xtrinode, "coordinator"),
		KafkaSchemasConfigMapWorker:  BuildKafkaSchemasConfigMap(xtrinode, "worker"),
		PasswordAuthSecret:           namedSecret(xtrinode, "trino-coverage-password"),
		GroupsAuthSecret:             namedSecret(xtrinode, "trino-coverage-groups"),
		CoordinatorPDB:               BuildCoordinatorPodDisruptionBudget(xtrinode),
		WorkerPDB:                    BuildWorkerPodDisruptionBudget(xtrinode),
		Ingress:                      BuildIngress(xtrinode),
		NetworkPolicy:                BuildNetworkPolicy(xtrinode),
		HorizontalPodAutoscaler:      BuildHorizontalPodAutoscaler(xtrinode),
		AccessControlConfigMapCoord:  namedConfigMap(xtrinode, "trino-coverage-access-control-volume-coordinator"),
		AccessControlConfigMapWorker: namedConfigMap(xtrinode, "trino-coverage-access-control-volume-worker"),
		ResourceGroupsConfigMapCoord: namedConfigMap(xtrinode, "trino-coverage-resource-groups-volume-coordinator"),
	}

	objects := set.AllResources()
	cli := resourceCoverageClient(t, objects...)

	require.NoError(t, DeleteTrinoResources(context.Background(), cli, set))
	for _, obj := range objects {
		got := obj.DeepCopyObject().(client.Object)
		err := cli.Get(context.Background(), client.ObjectKeyFromObject(obj), got)
		require.Error(t, err, "expected %s to be deleted", obj.GetName())
	}
}

func TestCleanupOldConfigMapRevisionsKeepsNewestPerRole(t *testing.T) {
	xtrinode := resourceCoverageBaseXTrinode()
	now := time.Now()
	objects := []client.Object{
		revisionConfigMap(xtrinode, "coordinator", "old-a", now.Add(-5*time.Hour)),
		revisionConfigMap(xtrinode, "coordinator", "old-b", now.Add(-4*time.Hour)),
		revisionConfigMap(xtrinode, "coordinator", "keep-a", now.Add(-3*time.Hour)),
		revisionConfigMap(xtrinode, "coordinator", "keep-b", now.Add(-2*time.Hour)),
		revisionConfigMap(xtrinode, "coordinator", "keep-c", now.Add(-time.Hour)),
		revisionConfigMap(xtrinode, "coordinator", "current", now),
		revisionConfigMap(xtrinode, "worker", "old-a", now.Add(-5*time.Hour)),
		revisionConfigMap(xtrinode, "worker", "old-b", now.Add(-4*time.Hour)),
		revisionConfigMap(xtrinode, "worker", "keep-a", now.Add(-3*time.Hour)),
		revisionConfigMap(xtrinode, "worker", "keep-b", now.Add(-2*time.Hour)),
		revisionConfigMap(xtrinode, "worker", "keep-c", now.Add(-time.Hour)),
		revisionConfigMap(xtrinode, "worker", "current", now),
	}
	cli := resourceCoverageClient(t, objects...)

	require.NoError(t, CleanupOldConfigMapRevisions(context.Background(), cli, xtrinode, "current"))

	for _, role := range []string{"coordinator", "worker"} {
		for _, rev := range []string{"old-a", "old-b"} {
			err := cli.Get(context.Background(), types.NamespacedName{Namespace: xtrinode.Namespace, Name: revisionConfigMapName(xtrinode, role, rev)}, &corev1.ConfigMap{})
			require.Error(t, err)
		}
		for _, rev := range []string{"keep-a", "keep-b", "keep-c", "current"} {
			err := cli.Get(context.Background(), types.NamespacedName{Namespace: xtrinode.Namespace, Name: revisionConfigMapName(xtrinode, role, rev)}, &corev1.ConfigMap{})
			require.NoError(t, err)
		}
	}
}

func TestCleanupOldConfigMapRevisionsKeepsDifferentCurrentRevisionPerRole(t *testing.T) {
	xtrinode := resourceCoverageBaseXTrinode()
	now := time.Now()
	objects := []client.Object{
		revisionConfigMap(xtrinode, "coordinator", "coord-old-a", now.Add(-5*time.Hour)),
		revisionConfigMap(xtrinode, "coordinator", "coord-old-b", now.Add(-4*time.Hour)),
		revisionConfigMap(xtrinode, "coordinator", "coord-old-c", now.Add(-3*time.Hour)),
		revisionConfigMap(xtrinode, "coordinator", "coord-old-d", now.Add(-2*time.Hour)),
		revisionConfigMap(xtrinode, "coordinator", "coord-current", now),
		revisionConfigMap(xtrinode, "worker", "worker-current", now.Add(-6*time.Hour)),
		revisionConfigMap(xtrinode, "worker", "worker-old-a", now.Add(-5*time.Hour)),
		revisionConfigMap(xtrinode, "worker", "worker-old-b", now.Add(-4*time.Hour)),
		revisionConfigMap(xtrinode, "worker", "worker-old-c", now.Add(-3*time.Hour)),
		revisionConfigMap(xtrinode, "worker", "worker-old-d", now.Add(-2*time.Hour)),
	}
	cli := resourceCoverageClient(t, objects...)

	require.NoError(t, CleanupOldConfigMapRevisionsForRoles(context.Background(), cli, xtrinode, "coord-current", "worker-current"))

	err := cli.Get(context.Background(), types.NamespacedName{
		Namespace: xtrinode.Namespace,
		Name:      revisionConfigMapName(xtrinode, "worker", "worker-current"),
	}, &corev1.ConfigMap{})
	require.NoError(t, err, "current worker ConfigMap must not be pruned when its revision differs from coordinator")
}

func resourceCoverageBaseXTrinode() *analyticsv1.XTrinode {
	return &analyticsv1.XTrinode{
		TypeMeta: metav1.TypeMeta{
			APIVersion: analyticsv1.GroupVersion.String(),
			Kind:       "XTrinode",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "coverage",
			Namespace: "team-a",
			UID:       "coverage-uid",
		},
		Spec: analyticsv1.XTrinodeSpec{Size: "s"},
	}
}

func resourceCoverageXTrinode() *analyticsv1.XTrinode {
	xtrinode := resourceCoverageBaseXTrinode()
	enabled := true
	xtrinode.Spec.KEDA = &analyticsv1.KEDASpec{
		Enabled: &enabled,
		JMXExporter: &analyticsv1.JMXExporterSpec{
			Enabled: true,
		},
	}
	xtrinode.Spec.HelmChartConfig = &analyticsv1.HelmChartConfigSpec{
		AccessControl: &analyticsv1.AccessControlSpec{
			Type:          "configmap",
			RefreshPeriod: "30s",
			ConfigFile:    "rules.json",
			Rules: map[string]string{
				"rules.json": `{"catalogs":[]}`,
			},
		},
		ServiceMonitor: &analyticsv1.ServiceMonitorSpec{Enabled: true},
		NetworkPolicy:  &analyticsv1.NetworkPolicySpec{Enabled: true},
		Worker: &analyticsv1.WorkerHelmConfigSpec{
			GracefulShutdown: &analyticsv1.GracefulShutdownSpec{
				Enabled:            true,
				GracePeriodSeconds: 11,
			},
		},
	}
	xtrinode.Spec.ValuesOverlay = mustValuesOverlay(map[string]interface{}{
		"image": map[string]interface{}{
			"repository": "trinodb/trino",
			"tag":        "480",
			"pullPolicy": "IfNotPresent",
		},
		"server": map[string]interface{}{
			"workers": int64(2),
			"config": map[string]interface{}{
				"authenticationType": "PASSWORD",
			},
			"autoscaling": map[string]interface{}{
				"enabled":                           true,
				"minReplicas":                       int64(1),
				"maxReplicas":                       int64(4),
				"targetCPUUtilizationPercentage":    int64(60),
				"targetMemoryUtilizationPercentage": "",
				"behavior": map[string]interface{}{
					"scaleDown": map[string]interface{}{
						"stabilizationWindowSeconds": int64(300),
						"selectPolicy":               "Min",
						"policies": []interface{}{
							map[string]interface{}{"type": "Pods", "value": int64(1), "periodSeconds": int64(60)},
						},
					},
				},
			},
		},
		"auth": map[string]interface{}{
			"passwordAuth":  "admin:password",
			"groups":        "admin:ops",
			"refreshPeriod": "10s",
		},
		"sessionProperties": map[string]interface{}{
			"type":                    "configmap",
			"sessionPropertiesConfig": `{"sessionProperties":[]}`,
		},
		"kafka": map[string]interface{}{
			"tableDescriptions": map[string]interface{}{
				"orders.json": "schema-json",
			},
		},
		"jmx": map[string]interface{}{
			"enabled":      true,
			"registryPort": int64(19080),
			"serverPort":   int64(19081),
			"exporter": map[string]interface{}{
				"enabled":          true,
				"port":             int64(15556),
				"configProperties": "hostPort: localhost:19080\nssl: false",
			},
		},
		"resourceGroups": map[string]interface{}{
			"type":                 "configmap",
			"resourceGroupsConfig": `{"rootGroups":[]}`,
		},
	})
	return xtrinode
}

func resourceCoverageClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, analyticsv1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, policyv1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, autoscalingv2.AddToScheme(scheme))
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func resourceNames(objects []client.Object) []string {
	names := make([]string, 0, len(objects))
	for _, obj := range objects {
		names = append(names, obj.GetName())
	}
	return names
}

func namedConfigMap(xtrinode *analyticsv1.XTrinode, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: xtrinode.Namespace,
			Labels:    TrinoLabels(xtrinode),
		},
		Data: map[string]string{"key": "value"},
	}
}

func namedSecret(xtrinode *analyticsv1.XTrinode, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: xtrinode.Namespace,
			Labels:    TrinoLabels(xtrinode),
		},
		Data: map[string][]byte{"key": []byte("value")},
	}
}

func revisionConfigMap(xtrinode *analyticsv1.XTrinode, role, revision string, created time.Time) *corev1.ConfigMap {
	cm := namedConfigMap(xtrinode, revisionConfigMapName(xtrinode, role, revision))
	cm.CreationTimestamp = metav1.NewTime(created)
	return cm
}

func revisionConfigMapName(xtrinode *analyticsv1.XTrinode, role, revision string) string {
	switch role {
	case "coordinator":
		return coordinatorConfigMapName(xtrinode, revision)
	case "worker":
		return workerConfigMapName(xtrinode, revision)
	default:
		return "trino-" + xtrinode.Name + "-" + role + "-" + revision
	}
}
