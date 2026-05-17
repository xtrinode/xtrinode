package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/go-logr/logr"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
)

// XTrinodeCatalogReconciler reconciles a XTrinodeCatalog object
type XTrinodeCatalogReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	EventRecorder events.Recorder // Event recorder for Kubernetes events (injected)
}

// +kubebuilder:rbac:groups=analytics.xtrinode.io,resources=xtrinodecatalogs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=analytics.xtrinode.io,resources=xtrinodecatalogs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=analytics.xtrinode.io,resources=xtrinodes,verbs=get;list;watch

// updateStatus updates the XTrinodeCatalog status with retry logic for conflict handling
func (r *XTrinodeCatalogReconciler) updateStatus(ctx context.Context, catalog *analyticsv1.XTrinodeCatalog, log logr.Logger) error {
	// Capture status mutations to reapply after refresh
	capturedPhase := catalog.Status.Phase
	capturedMessage := catalog.Status.Message
	capturedConfigMapName := catalog.Status.ConfigMapName
	capturedLastUpdated := catalog.Status.LastUpdated
	key := client.ObjectKeyFromObject(catalog)

	return updateStatusWithRetry(ctx, r.Client, r.Status(), key, log,
		func() client.Object { return &analyticsv1.XTrinodeCatalog{} },
		func(obj client.Object) error {
			c, ok := obj.(*analyticsv1.XTrinodeCatalog)
			if !ok {
				return fmt.Errorf("unexpected object type %T", obj)
			}
			// Reapply captured status changes
			c.Status.Phase = capturedPhase
			c.Status.Message = capturedMessage
			c.Status.ConfigMapName = capturedConfigMapName
			c.Status.LastUpdated = capturedLastUpdated
			return nil
		})
}

func (r *XTrinodeCatalogReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	var catalog analyticsv1.XTrinodeCatalog
	if err := r.Get(ctx, req.NamespacedName, &catalog); err != nil {
		if errors.IsNotFound(err) {
			// Catalog deleted - ConfigMap will be deleted via ownerReference
			// XTrinode controller's ConfigMap watch will automatically enqueue affected XTrinodes
			return ctrl.Result{}, nil
		}
		// Do not record events on the zero-value object; just log the fetch failure.
		log.Error(err, "unable to fetch XTrinodeCatalog", "namespace", req.Namespace, "name", req.Name)
		return ctrl.Result{}, err
	}

	// Record lifecycle events
	// Check if this is first reconciliation by checking if status phase is empty
	if catalog.Status.Phase == "" {
		// First time seeing this catalog - record Created event
		r.EventRecorder.Normal(&catalog, events.ReasonCreated, events.FormatMessage("XTrinodeCatalog %s/%s created", catalog.Namespace, catalog.Name))
	}
	// Note: Updated events are recorded when ConfigMap is updated (see updateConfigMap)

	// Execute reconciliation pipeline
	pipeline := NewCatalogReconciliationPipeline(r)
	result, err := pipeline.Execute(ctx, &catalog, log)
	if err != nil {
		r.EventRecorder.Warningf(&catalog, events.ReasonReconcileError, "Reconciliation failed: %v", err)
		return result, err
	}

	// Record successful reconciliation
	r.EventRecorder.Normal(&catalog, events.ReasonReconcileComplete, "XTrinodeCatalog reconciled successfully")
	log.Info("Successfully reconciled XTrinodeCatalog", "catalog", catalog.Name)
	return result, nil
}

// handleError updates catalog status to Error and returns error result
func (r *XTrinodeCatalogReconciler) handleError(ctx context.Context, catalog *analyticsv1.XTrinodeCatalog, log logr.Logger, err error, message string) (ctrl.Result, error) {
	log.Error(err, message)
	catalog.Status.Phase = "Error"
	catalog.Status.Message = err.Error()
	if updateErr := r.updateStatus(ctx, catalog, log); updateErr != nil {
		log.Error(updateErr, "failed to update status")
	}
	return ctrl.Result{}, err
}

// catalogDataHash computes a stable SHA-256 hash of the desired ConfigMap data.
// Used to short-circuit no-op updates and prevent downstream rollout storms.
func catalogDataHash(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		_, _ = h.Write([]byte(k))
		_, _ = h.Write([]byte("="))
		_, _ = h.Write([]byte(data[k]))
		_, _ = h.Write([]byte("\n"))
	}
	return hex.EncodeToString(h.Sum(nil))[:16] // 16 hex chars is sufficient
}

const catalogHashAnnotation = "xtrinode.analytics.xtrinode.io/catalog-hash"

// ensureConfigMap reconciles the ConfigMap with stable metadata and data hashing.
func (r *XTrinodeCatalogReconciler) ensureConfigMap(ctx context.Context, catalog *analyticsv1.XTrinodeCatalog, desired *corev1.ConfigMap, log logr.Logger) error {
	desiredHash := catalogDataHash(desired.Data)

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}

	result, err := controllerutil.CreateOrPatch(ctx, r.Client, existing, func() error {
		// Always repair metadata: labels, annotations, ownerRef
		// This ensures self-healing even if only metadata drifted
		existing.Labels = desired.Labels
		if existing.Annotations == nil {
			existing.Annotations = make(map[string]string)
		}
		existing.Annotations[catalogHashAnnotation] = desiredHash

		// SetControllerReference ensures correct ownerRef (handles TypeMeta safely)
		if err := controllerutil.SetControllerReference(catalog, existing, r.Scheme); err != nil {
			return err
		}

		// Skip data update if hash matches — avoids no-op writes that trigger downstream watches
		if existing.Data != nil && reflect.DeepEqual(existing.Data, desired.Data) {
			return nil
		}
		existing.Data = desired.Data
		return nil
	})
	if err != nil {
		log.Error(err, "failed to ensure ConfigMap")
		r.EventRecorder.Warningf(catalog, events.ReasonResourceApplyFailed,
			"Failed to ensure ConfigMap %s/%s: %v", desired.Namespace, desired.Name, err)
		return err
	}

	switch result {
	case controllerutil.OperationResultCreated:
		log.Info("Created ConfigMap", "name", desired.Name, "namespace", desired.Namespace)
		r.EventRecorder.Normalf(catalog, events.ReasonResourceCreated,
			"Created ConfigMap %s/%s", desired.Namespace, desired.Name)
	case controllerutil.OperationResultUpdated:
		log.Info("Updated ConfigMap", "name", desired.Name, "namespace", desired.Namespace)
		r.EventRecorder.Normalf(catalog, events.ReasonResourceUpdated,
			"Updated ConfigMap %s/%s", desired.Namespace, desired.Name)
	default:
		// OperationResultNone — no change
		log.V(1).Info("ConfigMap unchanged", "name", desired.Name)
	}

	return nil
}

// updateStatusToReady updates catalog status to Ready
func (r *XTrinodeCatalogReconciler) updateStatusToReady(ctx context.Context, catalog *analyticsv1.XTrinodeCatalog, configMapName string, log logr.Logger) error {
	catalog.Status.Phase = "Ready"
	catalog.Status.Message = fmt.Sprintf("ConfigMap %s applied successfully", configMapName)
	catalog.Status.ConfigMapName = configMapName
	now := metav1.Now()
	catalog.Status.LastUpdated = &now
	return r.updateStatus(ctx, catalog, log)
}

// generateConfigMap generates a ConfigMap from a XTrinodeCatalog
func (r *XTrinodeCatalogReconciler) generateConfigMap(catalog *analyticsv1.XTrinodeCatalog) (*corev1.ConfigMap, error) {
	// Determine catalog name from XTrinodeCatalog name
	catalogName := strings.TrimPrefix(catalog.Name, config.CatalogConfigMapPrefix)

	// Generate properties file content
	properties, err := r.generateProperties(catalog)
	if err != nil {
		return nil, fmt.Errorf("failed to generate properties: %w", err)
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      config.CatalogConfigMapPrefix + catalogName,
			Namespace: catalog.Namespace,
			Labels:    catalogConfigMapLabels(catalog),
			// Note: OwnerReference is set by ensureConfigMap via controllerutil.SetControllerReference,
			// which safely handles TypeMeta (avoids empty APIVersion/Kind on fetched objects).
		},
		Data: map[string]string{
			fmt.Sprintf("%s.properties", catalogName): properties,
		},
	}

	return configMap, nil
}

func catalogConfigMapLabels(catalog *analyticsv1.XTrinodeCatalog) map[string]string {
	labels := make(map[string]string, len(catalog.Spec.Labels)+3)
	for k, v := range catalog.Spec.Labels {
		labels[k] = v
	}
	labels["app"] = "trino"
	labels["xtrinode-catalog"] = catalog.Name
	labels["xtrinode-catalog-generated"] = "true"
	return labels
}

// generateProperties generates Trino catalog properties from XTrinodeCatalog spec
func (r *XTrinodeCatalogReconciler) generateProperties(catalog *analyticsv1.XTrinodeCatalog) (string, error) {
	if _, err := catalog.ValidateCreate(); err != nil {
		return "", fmt.Errorf("invalid XTrinodeCatalog spec: %w", err)
	}

	catalogCopy := catalog.DeepCopy()
	connector := &catalogCopy.Spec.Connector
	catalogName := strings.TrimPrefix(catalog.Name, config.CatalogConfigMapPrefix)

	connectorName, props, err := resolveConnector(connector, catalogName)
	if err != nil {
		return "", err
	}

	return r.buildPropertiesString(connectorName, props), nil
}

// ensurePropertiesMap ensures the properties map is not nil and returns a copy
// to avoid mutating the original spec properties
func ensurePropertiesMap(props map[string]string) map[string]string {
	if props == nil {
		return make(map[string]string)
	}
	// Copy the map to avoid in-place mutation of spec properties
	propsCopy := make(map[string]string, len(props))
	for k, v := range props {
		propsCopy[k] = v
	}
	return propsCopy
}

// buildPropertiesString builds the properties file content string
// Keys are sorted to ensure deterministic output and avoid spurious ConfigMap updates
func (r *XTrinodeCatalogReconciler) buildPropertiesString(connectorName string, props map[string]string) string {
	var properties strings.Builder
	fmt.Fprintf(&properties, "connector.name=%s\n", connectorName)

	// Sort keys for deterministic output
	keys := make([]string, 0, len(props))
	for key := range props {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	// Write properties in sorted order
	for _, key := range keys {
		fmt.Fprintf(&properties, "%s=%s\n", key, props[key])
	}

	return properties.String()
}

// calculateEnvVarName generates a consistent environment variable name for catalog properties
// Format: CATALOG_<CATALOG_NAME>_<PROPERTY_NAME> (uppercase, dashes/dots replaced with underscores)
func calculateEnvVarName(catalogName, propertyName string) string {
	// Remove catalog prefix if present
	catalogName = strings.TrimPrefix(catalogName, config.CatalogConfigMapPrefix)
	// Replace special characters with underscores
	catalogName = strings.NewReplacer(".", "_", "-", "_").Replace(catalogName)
	propertyName = strings.NewReplacer(".", "_", "-", "_").Replace(propertyName)
	return fmt.Sprintf("CATALOG_%s_%s", strings.ToUpper(catalogName), strings.ToUpper(propertyName))
}

// Note: XTrinode reconciliation is automatically triggered by the XTrinode controller's
// ConfigMap watch. When a XTrinodeCatalog is created/updated/deleted, the corresponding
// ConfigMap changes, and the XTrinode controller enqueues all XTrinodes in that namespace.
// No manual annotation-based triggering is needed.

func (r *XTrinodeCatalogReconciler) SetupWithManager(mgr ctrl.Manager, maxConcurrentReconciles int) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: maxConcurrentReconciles,
		}).
		For(&analyticsv1.XTrinodeCatalog{}, builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&corev1.ConfigMap{}). // Re-reconcile if owned ConfigMap is deleted/modified
		Complete(r)
}
