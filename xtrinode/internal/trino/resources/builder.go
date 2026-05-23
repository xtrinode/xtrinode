package resources

import (
	"context"
	"fmt"
	"sort"

	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/go-logr/logr"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/catalog"
	"github.com/xtrinode/xtrinode/internal/rollout"
	"github.com/xtrinode/xtrinode/internal/sizing"
)

// BuildTrinoResourceSet builds all Trino resources for a XTrinode
func BuildTrinoResourceSet(
	ctx context.Context,
	cli client.Client,
	xtrinode *analyticsv1.XTrinode,
	catalogs []string,
	operatorVersion string,
) (*TrinoResourceSet, error) {
	// Get size preset
	preset, ok := sizing.GetPreset(xtrinode.Spec.Size)
	if !ok {
		return nil, fmt.Errorf("invalid size preset: %s", xtrinode.Spec.Size)
	}

	// Sort catalogs for deterministic revision and resource generation.
	sortedCatalogs := make([]string, len(catalogs))
	copy(sortedCatalogs, catalogs)
	sort.Strings(sortedCatalogs)

	// Compute base revision (identity/debug - spec + operatorVersion)
	// This is used for resource naming and debugging, not rollout decisions
	baseRevision := GetXTrinodeRevision(xtrinode, operatorVersion, sortedCatalogs)

	// Compute per-component rollout hashes with content digests.
	catalogDigest, err := rollout.ComputeCatalogDigest(ctx, cli, xtrinode, sortedCatalogs)
	if err != nil {
		return nil, fmt.Errorf("failed to compute catalog digest: %w", err)
	}

	accessControlDigest, err := rollout.ComputeAccessControlDigest(ctx, cli, xtrinode)
	if err != nil {
		return nil, fmt.Errorf("failed to compute access control digest: %w", err)
	}

	sessionPropsDigest, err := rollout.ComputeSessionPropsDigest(ctx, cli, xtrinode)
	if err != nil {
		return nil, fmt.Errorf("failed to compute session props digest: %w", err)
	}

	// Compute TLS secret digest for cert rotation detection
	secretDigest, err := rollout.ComputeSecretDigest(ctx, cli, xtrinode)
	if err != nil {
		return nil, fmt.Errorf("failed to compute secret digest: %w", err)
	}

	// Catalog files and catalog-backed Secret env vars are present on workers too,
	// so catalog content changes must roll both roles.
	rollWorkersOnCatalogChange := true
	rolloutDigests := roleRolloutDigests{
		Catalog:       catalogDigest,
		AccessControl: accessControlDigest,
		SessionProps:  sessionPropsDigest,
		Secret:        secretDigest,
	}

	// Keep the resource revision tied to the caller-provided base revision.
	revision := baseRevision

	// Catalog ConfigMaps are managed by XTrinodeCatalog controller
	// Each catalog ConfigMap is named {CatalogConfigMapPrefix}{catalogName}
	// We don't create them here, just mount them via volume mounts

	// Extract catalog secret references for environment variable injection
	// This enables passwords to be injected as env vars instead of being in ConfigMaps
	// Use a no-op logger since we don't have access to the logger in this context
	catalogSecretEnvVars, err := catalog.ExtractCatalogSecretReferences(ctx, cli, xtrinode, logr.Discard())
	if err != nil {
		return nil, fmt.Errorf("failed to extract catalog secret references: %w", err)
	}

	// Build ConfigMaps with names derived from rendered ConfigMap data. The
	// resource revision remains the broad base revision for convergence/debugging,
	// but ConfigMap volume references only change when rendered config changes.
	coordinatorConfigMapData := buildCoordinatorConfigMapData(xtrinode, &preset, sortedCatalogs)
	coordinatorConfigMapRevision := configMapDataRevision(coordinatorConfigMapData)
	coordinatorConfigMap := buildCoordinatorConfigMapFromData(xtrinode, coordinatorConfigMapData, coordinatorConfigMapRevision, revision)

	// Render workers unless workers=0 and no autoscaler owns worker scale.
	shouldRenderWorker := true // Default to rendering workers
	valuesOverlay := xtrinode.Spec.GetValuesOverlayMap()
	if valuesOverlay != nil {
		if server, ok := valuesOverlay["server"].(map[string]interface{}); ok {
			autoscalerEnabled := isKEDAEnabled(xtrinode) || isNativeHPAEnabled(xtrinode)
			workersCount := -1 // -1 means not specified

			// Check workers count
			if workers, ok := ParseInt32(server["workers"]); ok {
				workersCount = int(workers)
			}

			minWorkers := int32(0)
			if xtrinode.Spec.MinWorkers != nil {
				minWorkers = *xtrinode.Spec.MinWorkers
			}

			// Only disable if workers=0, no autoscaler owns replicas, and no fixed floor is set.
			if workersCount == 0 && !autoscalerEnabled && minWorkers == 0 {
				shouldRenderWorker = false
			}
		}
	}

	var workerConfigMap *corev1.ConfigMap
	var workerDeployment *appsv1.Deployment
	var workerService *corev1.Service
	var workerMetricsService *corev1.Service
	var kafkaSchemasConfigMapWorker *corev1.ConfigMap
	var workerPDB *policyv1.PodDisruptionBudget
	var workerServiceMonitor runtime.Object
	var workerJMXExporterConfigMap *corev1.ConfigMap
	var workerRolloutHash string

	if shouldRenderWorker {
		workerConfigMapData := buildWorkerConfigMapData(xtrinode, &preset, sortedCatalogs)
		workerConfigMapRevision := configMapDataRevision(workerConfigMapData)
		workerConfigMap = buildWorkerConfigMapFromData(xtrinode, workerConfigMapData, workerConfigMapRevision, revision)

		draftWorkerDeployment, err := buildWorkerDeployment(
			xtrinode,
			&preset,
			workerConfigMap.Name,
			sortedCatalogs,
			revision,
			"",
			"",
			catalogSecretEnvVars,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to build worker Deployment: %w", err)
		}
		workerRolloutHash = workerPodRolloutHash(&draftWorkerDeployment.Spec.Template, rollWorkersOnCatalogChange, rolloutDigests)
		workerDeployment, err = buildWorkerDeployment(
			xtrinode,
			&preset,
			workerConfigMap.Name,
			sortedCatalogs,
			revision,
			workerRolloutHash,
			workerRolloutHash,
			catalogSecretEnvVars,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to build worker Deployment: %w", err)
		}

		workerService = BuildWorkerService(xtrinode)
		workerMetricsService = BuildWorkerMetricsService(xtrinode)
		workerPDB = BuildWorkerPodDisruptionBudget(xtrinode)
		workerServiceMonitor = BuildWorkerServiceMonitor(xtrinode)
		workerJMXExporterConfigMap = BuildJMXExporterConfigMap(xtrinode, "worker")

		// Always build Kafka schemas ConfigMap for worker (even empty), matching official Helm chart
		kafkaSchemasConfigMapWorker = BuildKafkaSchemasConfigMap(xtrinode, "worker")
	}

	// Build Deployments (with revision stamping and rollout hashes)
	draftCoordinatorDeployment, err := buildCoordinatorDeployment(
		xtrinode,
		&preset,
		coordinatorConfigMap.Name,
		sortedCatalogs,
		revision,
		"",
		"",
		catalogSecretEnvVars,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build coordinator Deployment: %w", err)
	}
	coordRolloutHash := coordinatorPodRolloutHash(&draftCoordinatorDeployment.Spec.Template, rolloutDigests)
	coordinatorDeployment, err := buildCoordinatorDeployment(
		xtrinode,
		&preset,
		coordinatorConfigMap.Name,
		sortedCatalogs,
		revision,
		coordRolloutHash,
		coordRolloutHash,
		catalogSecretEnvVars,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build coordinator Deployment: %w", err)
	}

	// Build Services
	coordinatorService := BuildCoordinatorService(xtrinode)

	// Build Metrics Services (for Prometheus scraping)
	coordinatorMetricsService := BuildCoordinatorMetricsService(xtrinode)

	// Build ServiceAccount
	serviceAccount := BuildServiceAccount(xtrinode)

	// Build session properties ConfigMap if configured
	var sessionPropertyConfigMap *corev1.ConfigMap
	if valuesOverlay != nil {
		if sessionProperties, ok := valuesOverlay["sessionProperties"].(map[string]interface{}); ok {
			if sessionType, ok := sessionProperties["type"].(string); ok && (sessionType == "configmap" || sessionType == "properties") {
				sessionPropertyConfigMap = BuildSessionPropertyConfigMap(xtrinode)
			}
		}
	}

	// Always build Kafka schemas ConfigMap for coordinator (even empty), matching official Helm chart
	kafkaSchemasConfigMapCoord := BuildKafkaSchemasConfigMap(xtrinode, "coordinator")

	// Build authentication Secrets if passwordAuth/groups are provided as strings
	passwordAuthSecret := BuildPasswordAuthSecret(xtrinode)
	groupsAuthSecret := BuildGroupsAuthSecret(xtrinode)

	// Build PodDisruptionBudgets (configurable via valuesOverlay)
	coordinatorPDB := BuildCoordinatorPodDisruptionBudget(xtrinode)

	// Build Ingress (if configured)
	ingress := BuildIngress(xtrinode)

	// Build NetworkPolicy (if configured)
	networkPolicy := BuildNetworkPolicy(xtrinode)

	// Build HorizontalPodAutoscaler (if configured, alternative to KEDA)
	horizontalPodAutoscaler := BuildHorizontalPodAutoscaler(xtrinode)

	// Build ServiceMonitors (if configured, Prometheus Operator)
	coordinatorServiceMonitor := BuildCoordinatorServiceMonitor(xtrinode)

	// Build JMX exporter ConfigMap for coordinator (if configured)
	coordinatorJMXExporterConfigMap := BuildJMXExporterConfigMap(xtrinode, "coordinator")

	// Build access control ConfigMaps (if configured)
	accessControlConfigMapCoord := BuildAccessControlConfigMapCoordinator(xtrinode)
	accessControlConfigMapWorker := BuildAccessControlConfigMapWorker(xtrinode)

	// Build resource groups ConfigMaps (if type == "configmap")
	resourceGroupsConfigMapCoord := BuildResourceGroupsConfigMapCoordinator(xtrinode)
	resourceGroupsConfigMapWorker := BuildResourceGroupsConfigMapWorker(xtrinode)

	return &TrinoResourceSet{
		CoordinatorDeployment:           coordinatorDeployment,
		WorkerDeployment:                workerDeployment,
		CoordinatorService:              coordinatorService,
		WorkerService:                   workerService,
		CoordinatorMetricsService:       coordinatorMetricsService,
		WorkerMetricsService:            workerMetricsService,
		CoordinatorConfigMap:            coordinatorConfigMap,
		WorkerConfigMap:                 workerConfigMap,
		CatalogConfigMap:                nil, // Catalogs are managed by XTrinodeCatalog controller
		ServiceAccount:                  serviceAccount,
		SessionPropertyConfigMap:        sessionPropertyConfigMap,
		KafkaSchemasConfigMapCoord:      kafkaSchemasConfigMapCoord,
		KafkaSchemasConfigMapWorker:     kafkaSchemasConfigMapWorker,
		PasswordAuthSecret:              passwordAuthSecret,
		GroupsAuthSecret:                groupsAuthSecret,
		CoordinatorPDB:                  coordinatorPDB,
		WorkerPDB:                       workerPDB,
		Ingress:                         ingress,
		NetworkPolicy:                   networkPolicy,
		HorizontalPodAutoscaler:         horizontalPodAutoscaler,
		CoordinatorServiceMonitor:       coordinatorServiceMonitor,
		WorkerServiceMonitor:            workerServiceMonitor,
		CoordinatorJMXExporterConfigMap: coordinatorJMXExporterConfigMap,
		WorkerJMXExporterConfigMap:      workerJMXExporterConfigMap,
		AccessControlConfigMapCoord:     accessControlConfigMapCoord,
		AccessControlConfigMapWorker:    accessControlConfigMapWorker,
		ResourceGroupsConfigMapCoord:    resourceGroupsConfigMapCoord,
		ResourceGroupsConfigMapWorker:   resourceGroupsConfigMapWorker,
	}, nil
}
