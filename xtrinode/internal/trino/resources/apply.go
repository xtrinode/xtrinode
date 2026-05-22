package resources

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// applyObject sets GVK on obj and patches with server-side apply (required for API server to accept Apply)
func applyObject(ctx context.Context, c client.Client, scheme *runtime.Scheme, obj client.Object, fieldOwner string, forceOwnership bool) error {
	gvk, err := apiutil.GVKForObject(obj, scheme)
	if err != nil {
		return fmt.Errorf("failed to get GVK: %w", err)
	}
	obj.GetObjectKind().SetGroupVersionKind(gvk)
	opts := []client.PatchOption{client.FieldOwner(fieldOwner)}
	if forceOwnership {
		opts = append(opts, client.ForceOwnership)
	}
	return c.Patch(ctx, obj, client.Apply, opts...)
}

func applyOptionalCRDObject(ctx context.Context, c client.Client, scheme *runtime.Scheme, obj client.Object, fieldOwner string, forceOwnership bool, resourceName string) error {
	err := applyObject(ctx, c, scheme, obj, fieldOwner, forceOwnership)
	if err == nil {
		return nil
	}
	if meta.IsNoMatchError(err) || strings.Contains(err.Error(), "no matches for kind") {
		log.FromContext(ctx).V(1).Info("Skipping optional CRD resource because its CRD is not installed", "resource", resourceName, "error", err)
		return nil
	}
	return err
}

// ApplyTrinoResources applies all Trino resources using server-side apply
func ApplyTrinoResources(
	ctx context.Context,
	c client.Client,
	scheme *runtime.Scheme,
	xtrinode *analyticsv1.XTrinode,
	resources *TrinoResourceSet,
) error {
	// Apply resources in dependency order:
	// 1. ServiceAccount (needed by pods)
	// 2. ConfigMaps (needed by pods)
	// 3. Services (needed for discovery)
	// 4. Deployments (depends on everything above)

	if resources.ServiceAccount != nil {
		if err := applyObject(ctx, c, scheme, resources.ServiceAccount, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply ServiceAccount: %w", err)
		}
	}

	if resources.CoordinatorConfigMap != nil {
		if err := applyObject(ctx, c, scheme, resources.CoordinatorConfigMap, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply CoordinatorConfigMap: %w", err)
		}
	}

	if resources.WorkerConfigMap != nil {
		if err := applyObject(ctx, c, scheme, resources.WorkerConfigMap, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply WorkerConfigMap: %w", err)
		}
	}

	if resources.CatalogConfigMap != nil {
		if err := applyObject(ctx, c, scheme, resources.CatalogConfigMap, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply CatalogConfigMap: %w", err)
		}
	}

	// Apply ConfigMaps and Secrets before Deployments so rollouts mount current config.
	// Apply session properties ConfigMap if configured
	if resources.SessionPropertyConfigMap != nil {
		if err := applyObject(ctx, c, scheme, resources.SessionPropertyConfigMap, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply SessionPropertyConfigMap: %w", err)
		}
	}

	// Apply Kafka schemas ConfigMaps if configured
	if resources.KafkaSchemasConfigMapCoord != nil {
		if err := applyObject(ctx, c, scheme, resources.KafkaSchemasConfigMapCoord, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply KafkaSchemasConfigMapCoord: %w", err)
		}
	}
	if resources.KafkaSchemasConfigMapWorker != nil {
		if err := applyObject(ctx, c, scheme, resources.KafkaSchemasConfigMapWorker, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply KafkaSchemasConfigMapWorker: %w", err)
		}
	}

	// Apply JMX exporter ConfigMaps if configured
	if resources.CoordinatorJMXExporterConfigMap != nil {
		if err := applyObject(ctx, c, scheme, resources.CoordinatorJMXExporterConfigMap, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply CoordinatorJMXExporterConfigMap: %w", err)
		}
	}
	if resources.WorkerJMXExporterConfigMap != nil {
		if err := applyObject(ctx, c, scheme, resources.WorkerJMXExporterConfigMap, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply WorkerJMXExporterConfigMap: %w", err)
		}
	}

	// Apply access control ConfigMaps if configured
	if resources.AccessControlConfigMapCoord != nil {
		if err := applyObject(ctx, c, scheme, resources.AccessControlConfigMapCoord, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply AccessControlConfigMapCoord: %w", err)
		}
	}
	if resources.AccessControlConfigMapWorker != nil {
		if err := applyObject(ctx, c, scheme, resources.AccessControlConfigMapWorker, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply AccessControlConfigMapWorker: %w", err)
		}
	}

	// Apply resource groups ConfigMaps if configured
	if resources.ResourceGroupsConfigMapCoord != nil {
		if err := applyObject(ctx, c, scheme, resources.ResourceGroupsConfigMapCoord, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply ResourceGroupsConfigMapCoord: %w", err)
		}
	}
	if resources.ResourceGroupsConfigMapWorker != nil {
		if err := applyObject(ctx, c, scheme, resources.ResourceGroupsConfigMapWorker, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply ResourceGroupsConfigMapWorker: %w", err)
		}
	}

	// Apply authentication Secrets if passwordAuth/groups are provided as strings
	if resources.PasswordAuthSecret != nil {
		if err := applyObject(ctx, c, scheme, resources.PasswordAuthSecret, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply PasswordAuthSecret: %w", err)
		}
	}
	if resources.GroupsAuthSecret != nil {
		if err := applyObject(ctx, c, scheme, resources.GroupsAuthSecret, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply GroupsAuthSecret: %w", err)
		}
	}

	if resources.CoordinatorService != nil {
		if err := applyObject(ctx, c, scheme, resources.CoordinatorService, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply CoordinatorService: %w", err)
		}
	}

	if resources.WorkerService != nil {
		if err := applyObject(ctx, c, scheme, resources.WorkerService, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply WorkerService: %w", err)
		}
	}

	// Apply metrics services (for Prometheus scraping)
	if resources.CoordinatorMetricsService != nil {
		if err := applyObject(ctx, c, scheme, resources.CoordinatorMetricsService, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply CoordinatorMetricsService: %w", err)
		}
	}

	if resources.WorkerMetricsService != nil {
		if err := applyObject(ctx, c, scheme, resources.WorkerMetricsService, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply WorkerMetricsService: %w", err)
		}
	}

	if resources.CoordinatorDeployment != nil {
		// Don't force ownership on Deployments - allows HPA to manage replicas
		if err := applyObject(ctx, c, scheme, resources.CoordinatorDeployment, config.FieldOwner, false); err != nil {
			return fmt.Errorf("failed to apply CoordinatorDeployment: %w", err)
		}
	}

	// If so, DO NOT manage .spec.replicas field to avoid fighting with KEDA/HPA.
	if resources.WorkerDeployment != nil {
		autoscalingEnabled := isKEDAEnabled(xtrinode) || isNativeHPAEnabled(xtrinode)

		if autoscalingEnabled {
			// Remove .spec.replicas from the apply to let the autoscaler manage it.
			// Everything else (template, selector, etc.) is still applied.
			resources.WorkerDeployment.Spec.Replicas = nil
		}

		// Don't force ownership on Deployments - allows HPA/KEDA to manage replicas
		if err := applyObject(ctx, c, scheme, resources.WorkerDeployment, config.FieldOwner, false); err != nil {
			return fmt.Errorf("failed to apply WorkerDeployment: %w", err)
		}
	}

	// Apply PodDisruptionBudgets (after Deployments are created)
	if resources.CoordinatorPDB != nil {
		if err := applyObject(ctx, c, scheme, resources.CoordinatorPDB, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply CoordinatorPDB: %w", err)
		}
	}
	if resources.WorkerPDB != nil {
		if err := applyObject(ctx, c, scheme, resources.WorkerPDB, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply WorkerPDB: %w", err)
		}
	}

	// Apply Ingress (after Services are created)
	if resources.Ingress != nil {
		if err := applyObject(ctx, c, scheme, resources.Ingress, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply Ingress: %w", err)
		}
	}

	// Apply NetworkPolicy (after Services are created)
	if resources.NetworkPolicy != nil {
		if err := applyObject(ctx, c, scheme, resources.NetworkPolicy, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply NetworkPolicy: %w", err)
		}
	}

	// Apply HorizontalPodAutoscaler (after Deployment is created)
	if resources.HorizontalPodAutoscaler != nil {
		if err := applyObject(ctx, c, scheme, resources.HorizontalPodAutoscaler, config.FieldOwner, true); err != nil {
			return fmt.Errorf("failed to apply HorizontalPodAutoscaler: %w", err)
		}
	}

	// Apply ServiceMonitors (after Services are created)
	if resources.CoordinatorServiceMonitor != nil {
		if obj, ok := resources.CoordinatorServiceMonitor.(client.Object); ok {
			if err := applyOptionalCRDObject(ctx, c, scheme, obj, config.FieldOwner, true, "CoordinatorServiceMonitor"); err != nil {
				return fmt.Errorf("failed to apply CoordinatorServiceMonitor: %w", err)
			}
		}
	}
	if resources.WorkerServiceMonitor != nil {
		if obj, ok := resources.WorkerServiceMonitor.(client.Object); ok {
			if err := applyOptionalCRDObject(ctx, c, scheme, obj, config.FieldOwner, true, "WorkerServiceMonitor"); err != nil {
				return fmt.Errorf("failed to apply WorkerServiceMonitor: %w", err)
			}
		}
	}

	// Clean up old ConfigMap revisions to prevent accumulation.
	// Extract current revision from ConfigMap name
	var currentRevision string
	if resources.CoordinatorConfigMap != nil {
		// Extract revision from name: trino-{name}-coordinator-{revision}
		name := resources.CoordinatorConfigMap.Name
		parts := strings.Split(name, "-")
		if len(parts) > 0 {
			currentRevision = parts[len(parts)-1]
		}
	}

	if currentRevision != "" {
		if err := CleanupOldConfigMapRevisions(ctx, c, xtrinode, currentRevision); err != nil {
			log.FromContext(ctx).V(1).Info("ConfigMap revision cleanup failed",
				"namespace", xtrinode.Namespace,
				"name", xtrinode.Name,
				"revision", currentRevision,
				"error", err)
		}
	}

	if err := pruneDisabledTrinoResources(ctx, c, xtrinode, resources); err != nil {
		return fmt.Errorf("failed to prune disabled Trino resources: %w", err)
	}

	return nil
}

// DeleteTrinoResources deletes all Trino resources
func DeleteTrinoResources(
	ctx context.Context,
	c client.Client,
	resources *TrinoResourceSet,
) error {
	// Delete in reverse dependency order:
	// 1. Deployments
	// 2. Services
	// 3. ConfigMaps
	// 4. ServiceAccount

	// Ignore already-deleted resources so finalizers do not hang.
	if resources.WorkerDeployment != nil {
		if err := c.Delete(ctx, resources.WorkerDeployment); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete WorkerDeployment: %w", err)
		}
	}

	if resources.CoordinatorDeployment != nil {
		if err := c.Delete(ctx, resources.CoordinatorDeployment); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete CoordinatorDeployment: %w", err)
		}
	}

	// Delete metrics services first (before main services)
	if resources.WorkerMetricsService != nil {
		if err := c.Delete(ctx, resources.WorkerMetricsService); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete WorkerMetricsService: %w", err)
		}
	}

	if resources.CoordinatorMetricsService != nil {
		if err := c.Delete(ctx, resources.CoordinatorMetricsService); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete CoordinatorMetricsService: %w", err)
		}
	}

	if resources.WorkerService != nil {
		if err := c.Delete(ctx, resources.WorkerService); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete WorkerService: %w", err)
		}
	}

	if resources.CoordinatorService != nil {
		if err := c.Delete(ctx, resources.CoordinatorService); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete CoordinatorService: %w", err)
		}
	}

	if resources.CatalogConfigMap != nil {
		if err := c.Delete(ctx, resources.CatalogConfigMap); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete CatalogConfigMap: %w", err)
		}
	}

	if resources.WorkerConfigMap != nil {
		if err := c.Delete(ctx, resources.WorkerConfigMap); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete WorkerConfigMap: %w", err)
		}
	}

	if resources.CoordinatorConfigMap != nil {
		if err := c.Delete(ctx, resources.CoordinatorConfigMap); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete CoordinatorConfigMap: %w", err)
		}
	}

	if resources.ServiceAccount != nil {
		if err := c.Delete(ctx, resources.ServiceAccount); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete ServiceAccount: %w", err)
		}
	}

	// Delete session properties ConfigMap if configured
	if resources.SessionPropertyConfigMap != nil {
		if err := c.Delete(ctx, resources.SessionPropertyConfigMap); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete SessionPropertyConfigMap: %w", err)
		}
	}

	// Delete Kafka schemas ConfigMaps if configured
	if resources.KafkaSchemasConfigMapCoord != nil {
		if err := c.Delete(ctx, resources.KafkaSchemasConfigMapCoord); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete KafkaSchemasConfigMapCoord: %w", err)
		}
	}
	if resources.KafkaSchemasConfigMapWorker != nil {
		if err := c.Delete(ctx, resources.KafkaSchemasConfigMapWorker); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete KafkaSchemasConfigMapWorker: %w", err)
		}
	}

	// Delete JMX exporter ConfigMaps if configured
	if resources.CoordinatorJMXExporterConfigMap != nil {
		if err := c.Delete(ctx, resources.CoordinatorJMXExporterConfigMap); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete CoordinatorJMXExporterConfigMap: %w", err)
		}
	}
	if resources.WorkerJMXExporterConfigMap != nil {
		if err := c.Delete(ctx, resources.WorkerJMXExporterConfigMap); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete WorkerJMXExporterConfigMap: %w", err)
		}
	}

	// Delete access control ConfigMaps if configured
	if resources.AccessControlConfigMapCoord != nil {
		if err := c.Delete(ctx, resources.AccessControlConfigMapCoord); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete AccessControlConfigMapCoord: %w", err)
		}
	}
	if resources.AccessControlConfigMapWorker != nil {
		if err := c.Delete(ctx, resources.AccessControlConfigMapWorker); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete AccessControlConfigMapWorker: %w", err)
		}
	}

	// Delete resource groups ConfigMaps if configured
	if resources.ResourceGroupsConfigMapCoord != nil {
		if err := c.Delete(ctx, resources.ResourceGroupsConfigMapCoord); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete ResourceGroupsConfigMapCoord: %w", err)
		}
	}
	if resources.ResourceGroupsConfigMapWorker != nil {
		if err := c.Delete(ctx, resources.ResourceGroupsConfigMapWorker); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete ResourceGroupsConfigMapWorker: %w", err)
		}
	}

	// Delete authentication Secrets if passwordAuth/groups are provided as strings
	if resources.PasswordAuthSecret != nil {
		if err := c.Delete(ctx, resources.PasswordAuthSecret); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete PasswordAuthSecret: %w", err)
		}
	}
	if resources.GroupsAuthSecret != nil {
		if err := c.Delete(ctx, resources.GroupsAuthSecret); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete GroupsAuthSecret: %w", err)
		}
	}

	// Delete Ingress (before Services are deleted)
	if resources.Ingress != nil {
		if err := c.Delete(ctx, resources.Ingress); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete Ingress: %w", err)
		}
	}

	// Delete NetworkPolicy (before Services are deleted)
	if resources.NetworkPolicy != nil {
		if err := c.Delete(ctx, resources.NetworkPolicy); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete NetworkPolicy: %w", err)
		}
	}

	// Delete HorizontalPodAutoscaler (before Deployment is deleted)
	if resources.HorizontalPodAutoscaler != nil {
		if err := c.Delete(ctx, resources.HorizontalPodAutoscaler); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete HorizontalPodAutoscaler: %w", err)
		}
	}

	// Delete ServiceMonitors (before Services are deleted)
	if resources.CoordinatorServiceMonitor != nil {
		if obj, ok := resources.CoordinatorServiceMonitor.(client.Object); ok {
			if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
				if meta.IsNoMatchError(err) || strings.Contains(err.Error(), "no matches for kind") {
					log.FromContext(ctx).V(1).Info("Skipping optional ServiceMonitor deletion because its CRD is not installed", "resource", "CoordinatorServiceMonitor", "error", err)
				} else {
					return fmt.Errorf("failed to delete CoordinatorServiceMonitor: %w", err)
				}
			}
		}
	}
	if resources.WorkerServiceMonitor != nil {
		if obj, ok := resources.WorkerServiceMonitor.(client.Object); ok {
			if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
				if meta.IsNoMatchError(err) || strings.Contains(err.Error(), "no matches for kind") {
					log.FromContext(ctx).V(1).Info("Skipping optional ServiceMonitor deletion because its CRD is not installed", "resource", "WorkerServiceMonitor", "error", err)
				} else {
					return fmt.Errorf("failed to delete WorkerServiceMonitor: %w", err)
				}
			}
		}
	}

	// Delete PodDisruptionBudgets (before Deployments are deleted)
	if resources.WorkerPDB != nil {
		if err := c.Delete(ctx, resources.WorkerPDB); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete WorkerPDB: %w", err)
		}
	}
	if resources.CoordinatorPDB != nil {
		if err := c.Delete(ctx, resources.CoordinatorPDB); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete CoordinatorPDB: %w", err)
		}
	}

	return nil
}
