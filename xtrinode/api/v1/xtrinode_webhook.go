package v1

import (
	"context"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/xtrinode/xtrinode/internal/config"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	xtrinodelog = logf.Log.WithName("xtrinode-resource")
)

const valuesOverlayRBACResource = "xtrinodes/valuesoverlay"

// SetupWebhookWithManager sets up the webhook with the manager
func (t *XTrinode) SetupWebhookWithManager(mgr ctrl.Manager) error {
	hook := &XTrinodeWebhook{
		valuesOverlayAuthorizer: subjectAccessReviewValuesOverlayAuthorizer{client: mgr.GetClient()},
	}
	return ctrl.NewWebhookManagedBy(mgr).
		For(t).
		WithDefaulter(hook).
		WithValidator(hook).
		Complete()
}

// XTrinodeWebhook adapts XTrinode's defaulting and validation methods to the
// controller-runtime admission interfaces used by current releases.
// +kubebuilder:object:generate=false
type XTrinodeWebhook struct {
	valuesOverlayAuthorizer valuesOverlayAuthorizer
}

// +kubebuilder:object:generate=false
type valuesOverlayAuthorizer interface {
	Allowed(ctx context.Context, req *admission.Request, namespace string) (allowed bool, reason string, err error)
}

// +kubebuilder:object:generate=false
type subjectAccessReviewValuesOverlayAuthorizer struct {
	client client.Client
}

func (a subjectAccessReviewValuesOverlayAuthorizer) Allowed(ctx context.Context, req *admission.Request, namespace string) (allowed bool, reason string, err error) {
	if a.client == nil {
		return false, "valuesOverlay admission authorizer is not configured", nil
	}

	sar := &authorizationv1.SubjectAccessReview{
		Spec: authorizationv1.SubjectAccessReviewSpec{
			User:   req.UserInfo.Username,
			Groups: req.UserInfo.Groups,
			UID:    req.UserInfo.UID,
			ResourceAttributes: &authorizationv1.ResourceAttributes{
				Namespace:   namespace,
				Verb:        "update",
				Group:       "analytics.xtrinode.io",
				Resource:    "xtrinodes",
				Subresource: "valuesoverlay",
			},
		},
	}
	if len(req.UserInfo.Extra) > 0 {
		sar.Spec.Extra = make(map[string]authorizationv1.ExtraValue, len(req.UserInfo.Extra))
		for key, values := range req.UserInfo.Extra {
			sar.Spec.Extra[key] = authorizationv1.ExtraValue(values)
		}
	}

	if err := a.client.Create(ctx, sar); err != nil {
		return false, "", fmt.Errorf("failed to evaluate valuesOverlay authorization: %w", err)
	}
	if sar.Status.Allowed {
		return true, "", nil
	}
	if sar.Status.Reason != "" {
		return false, sar.Status.Reason, nil
	}
	if sar.Status.EvaluationError != "" {
		return false, sar.Status.EvaluationError, nil
	}
	return false, fmt.Sprintf("user lacks update permission on analytics.xtrinode.io/%s", valuesOverlayRBACResource), nil
}

func (w *XTrinodeWebhook) Default(_ context.Context, obj runtime.Object) error {
	xtrinode, ok := obj.(*XTrinode)
	if !ok {
		return fmt.Errorf("expected XTrinode, got %T", obj)
	}
	xtrinode.Default()
	return nil
}

func (w *XTrinodeWebhook) ValidateCreate(ctx context.Context, obj runtime.Object) (admission.Warnings, error) {
	xtrinode, ok := obj.(*XTrinode)
	if !ok {
		return nil, fmt.Errorf("expected XTrinode, got %T", obj)
	}
	warnings, err := xtrinode.ValidateCreate()
	if err != nil {
		return warnings, err
	}
	if err := w.validatePrivilegedSpecAdmission(ctx, nil, xtrinode); err != nil {
		return warnings, err
	}
	return warnings, nil
}

func (w *XTrinodeWebhook) ValidateUpdate(ctx context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	xtrinode, ok := newObj.(*XTrinode)
	if !ok {
		return nil, fmt.Errorf("expected new XTrinode, got %T", newObj)
	}
	warnings, err := xtrinode.ValidateUpdate(oldObj)
	if err != nil {
		return warnings, err
	}
	oldXTrinode, ok := oldObj.(*XTrinode)
	if !ok {
		return warnings, fmt.Errorf("expected old object to be of type XTrinode")
	}
	if err := w.validatePrivilegedSpecAdmission(ctx, oldXTrinode, xtrinode); err != nil {
		return warnings, err
	}
	return warnings, nil
}

func (w *XTrinodeWebhook) ValidateDelete(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	xtrinode, ok := obj.(*XTrinode)
	if !ok {
		return nil, fmt.Errorf("expected XTrinode, got %T", obj)
	}
	return xtrinode.ValidateDelete()
}

func (w *XTrinodeWebhook) validatePrivilegedSpecAdmission(ctx context.Context, oldObj, newObj *XTrinode) error {
	reasons := privilegedSpecChangeReasons(oldObj, newObj)
	if len(reasons) == 0 {
		return nil
	}

	path := field.NewPath("spec")
	req, err := admission.RequestFromContext(ctx)
	if err != nil {
		return apierrors.NewInvalid(
			schema.GroupKind{Group: "analytics.xtrinode.io", Kind: "XTrinode"},
			newObj.Name,
			field.ErrorList{
				field.Forbidden(path, fmt.Sprintf("privileged fields (%s) require admission request user info", strings.Join(reasons, ", "))),
			},
		)
	}
	if w.valuesOverlayAuthorizer == nil {
		return apierrors.NewInvalid(
			schema.GroupKind{Group: "analytics.xtrinode.io", Kind: "XTrinode"},
			newObj.Name,
			field.ErrorList{
				field.Forbidden(path, fmt.Sprintf("privileged fields (%s) require admission authorization, but it is not configured", strings.Join(reasons, ", "))),
			},
		)
	}

	allowed, reason, err := w.valuesOverlayAuthorizer.Allowed(ctx, &req, newObj.Namespace)
	if err != nil {
		return apierrors.NewInvalid(
			schema.GroupKind{Group: "analytics.xtrinode.io", Kind: "XTrinode"},
			newObj.Name,
			field.ErrorList{
				field.InternalError(path, err),
			},
		)
	}
	if allowed {
		return nil
	}

	message := fmt.Sprintf("privileged fields (%s) require update permission on analytics.xtrinode.io/%s", strings.Join(reasons, ", "), valuesOverlayRBACResource)
	if strings.TrimSpace(reason) != "" {
		message = fmt.Sprintf("%s: %s", message, reason)
	}
	return apierrors.NewInvalid(
		schema.GroupKind{Group: "analytics.xtrinode.io", Kind: "XTrinode"},
		newObj.Name,
		field.ErrorList{
			field.Forbidden(path, message),
		},
	)
}

func privilegedSpecChangeReasons(oldObj, newObj *XTrinode) []string {
	if newObj == nil {
		return nil
	}
	var reasons []string
	if valuesOverlayChanged(oldObj, newObj) {
		reasons = append(reasons, "spec.valuesOverlay")
	}
	if privilegedHelmChartConfigChanged(oldObj, newObj) {
		reasons = append(reasons, "spec.helmChartConfig")
	}
	return reasons
}

func valuesOverlayChanged(oldObj, newObj *XTrinode) bool {
	if newObj == nil {
		return false
	}
	if oldObj == nil {
		return newObj.Spec.ValuesOverlay != nil
	}
	return compareValuesOverlay(oldObj.Spec.ValuesOverlay, newObj.Spec.ValuesOverlay)
}

func privilegedHelmChartConfigChanged(oldObj, newObj *XTrinode) bool {
	if newObj == nil {
		return false
	}
	if oldObj == nil {
		return newObj.Spec.HelmChartConfig != nil
	}
	return !reflect.DeepEqual(oldObj.Spec.HelmChartConfig, newObj.Spec.HelmChartConfig)
}

// +kubebuilder:webhook:path=/mutate-analytics-xtrinode-io-v1-xtrinode,mutating=true,failurePolicy=fail,sideEffects=None,groups=analytics.xtrinode.io,resources=xtrinodes,verbs=create;update,versions=v1,name=mxtrinode.kb.io,admissionReviewVersions=v1

// Default implements webhook.Defaulter so a webhook will be registered for the type
func (t *XTrinode) Default() {
	xtrinodelog.Info("default", "name", t.Name)

	// Set default minWorkers to 0 if not specified
	if t.Spec.MinWorkers == nil {
		defaultMin := int32(0)
		t.Spec.MinWorkers = &defaultMin
	}

	// Set default auto-suspend threshold if not specified.
	if t.Spec.AutoSuspendAfter == nil {
		t.Spec.AutoSuspendAfter = &metav1.Duration{Duration: 5 * time.Minute}
	}

	// Set default node pool name if not specified
	if t.Spec.NodePool != nil && t.Spec.NodePool.Name == "" {
		t.Spec.NodePool.Name = fmt.Sprintf("%s-pool", t.Name)
	}

	// Set default minNodes to 0 if not specified
	if t.Spec.NodePool != nil && t.Spec.NodePool.MinNodes == nil {
		defaultMinNodes := int32(0)
		t.Spec.NodePool.MinNodes = &defaultMinNodes
	}

	// Set default maxNodes based on maxWorkers if not specified
	if t.Spec.NodePool != nil && t.Spec.NodePool.MaxNodes == nil {
		// Estimate: maxNodes = ceil(maxWorkers / workers_per_node)
		// Assume 1 worker per node for safety
		maxNodes := int32(10)
		if t.Spec.MaxWorkers != nil {
			maxNodes = *t.Spec.MaxWorkers
		}
		t.Spec.NodePool.MaxNodes = &maxNodes
	}

	// Set default OS disk size if not specified
	if t.Spec.NodePool != nil && t.Spec.NodePool.OSDiskGB == nil {
		defaultOSDisk := int32(128)
		t.Spec.NodePool.OSDiskGB = &defaultOSDisk
	}

	// Keep deletion semantics explicit after defaulting.
	if t.Spec.NodePool != nil && t.Spec.NodePool.DeletionPolicy == "" {
		t.Spec.NodePool.DeletionPolicy = NodePoolDeletionPolicyDelete
	}

	// Auto-populate machine type from size preset if not specified
	if t.Spec.NodePool != nil && t.Spec.Size != "" {
		provider := strings.ToLower(t.Spec.NodePool.Provider)
		recommendedMachineType, recommendationSource := t.recommendedNodePoolMachineType(provider)

		if recommendedMachineType != "" {
			switch provider {
			case "azure":
				if t.Spec.NodePool.Azure == nil {
					t.Spec.NodePool.Azure = &AzureNodePoolSpec{}
				}
				if t.Spec.NodePool.Azure.VMSize == "" {
					t.Spec.NodePool.Azure.VMSize = recommendedMachineType
					xtrinodelog.Info("auto-populated Azure vmSize from runtime recommendation",
						"source", recommendationSource, "vmSize", recommendedMachineType)
				}
			case "aws":
				if t.Spec.NodePool.AWS == nil {
					t.Spec.NodePool.AWS = &AWSNodePoolSpec{}
				}
				if t.Spec.NodePool.AWS.InstanceType == "" {
					t.Spec.NodePool.AWS.InstanceType = recommendedMachineType
					xtrinodelog.Info("auto-populated AWS instanceType from runtime recommendation",
						"source", recommendationSource, "instanceType", recommendedMachineType)
				}
			case "gcp":
				if t.Spec.NodePool.GCP == nil {
					t.Spec.NodePool.GCP = &GCPNodePoolSpec{}
				}
				if t.Spec.NodePool.GCP.MachineType == "" {
					t.Spec.NodePool.GCP.MachineType = recommendedMachineType
					xtrinodelog.Info("auto-populated GCP machineType from runtime recommendation",
						"source", recommendationSource, "machineType", recommendedMachineType)
				}
			}
		}
	}
}

// +kubebuilder:webhook:path=/validate-analytics-xtrinode-io-v1-xtrinode,mutating=false,failurePolicy=fail,sideEffects=None,groups=analytics.xtrinode.io,resources=xtrinodes,verbs=create;update,versions=v1,name=vxtrinode.kb.io,admissionReviewVersions=v1

// ValidateCreate implements webhook.Validator so a webhook will be registered for the type
func (t *XTrinode) ValidateCreate() (admission.Warnings, error) {
	xtrinodelog.Info("validate create", "name", t.Name)
	return t.validateCreateWarnings(), t.validateXTrinode()
}

// ValidateUpdate implements webhook.Validator so a webhook will be registered for the type
func (t *XTrinode) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	xtrinodelog.Info("validate update", "name", t.Name)
	oldXTrinode, ok := old.(*XTrinode)
	if !ok {
		return nil, fmt.Errorf("expected old object to be of type XTrinode")
	}
	warnings, err := t.validateXTrinodeUpdate(oldXTrinode)
	return warnings, err
}

// ValidateDelete implements webhook.Validator so a webhook will be registered for the type
func (t *XTrinode) ValidateDelete() (admission.Warnings, error) {
	xtrinodelog.Info("validate delete", "name", t.Name)
	// No validation needed for delete
	return nil, nil
}

func (t *XTrinode) validateCreateWarnings() admission.Warnings {
	var warnings admission.Warnings
	if t.Spec.ValuesOverlay != nil {
		warnings = append(warnings, buildValuesOverlayChangeWarning())
	}
	warnings = append(warnings, nodePoolPlacementWarnings(t)...)
	warnings = append(warnings, nodePoolFitWarnings(t)...)
	return warnings
}

// validateXTrinode validates a XTrinode spec
func (t *XTrinode) validateXTrinode() error {
	var allErrs field.ErrorList

	// Validate size
	if t.Spec.Size == "" {
		allErrs = append(allErrs, field.Required(field.NewPath("spec.size"), "size is required"))
	} else if !config.ValidSizes[strings.ToLower(t.Spec.Size)] {
		allErrs = append(allErrs, field.Invalid(
			field.NewPath("spec.size"),
			t.Spec.Size,
			fmt.Sprintf("size must be one of: %v", config.SizeList)))
	}

	// Validate maxWorkers
	if t.Spec.MaxWorkers != nil {
		if *t.Spec.MaxWorkers < 0 {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec.maxWorkers"),
				*t.Spec.MaxWorkers,
				"maxWorkers must be at least 0"))
		}
		if *t.Spec.MaxWorkers > 500 {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec.maxWorkers"),
				*t.Spec.MaxWorkers,
				"maxWorkers must be at most 500"))
		}
	}

	// Validate minWorkers
	if t.Spec.MinWorkers != nil {
		if *t.Spec.MinWorkers < 0 {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec.minWorkers"),
				*t.Spec.MinWorkers,
				"minWorkers must be at least 0"))
		}
		if t.Spec.MaxWorkers != nil && *t.Spec.MinWorkers > *t.Spec.MaxWorkers {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec.minWorkers"),
				*t.Spec.MinWorkers,
				"minWorkers must be less than or equal to maxWorkers"))
		}
	}

	// Validate nodePool
	if t.Spec.NodePool != nil {
		allErrs = append(allErrs, t.validateNodePool(field.NewPath("spec.nodePool"))...)
	}

	// Validate routing
	if t.Spec.Routing != nil {
		allErrs = append(allErrs, t.validateRouting(field.NewPath("spec.routing"))...)
	}

	// Validate typed resources and placement
	if t.Spec.Resources != nil {
		allErrs = append(allErrs, t.validateRuntimeResources(field.NewPath("spec.resources"))...)
	}
	if t.Spec.Placement != nil {
		allErrs = append(allErrs, t.validatePlacement(field.NewPath("spec.placement"))...)
	}
	allErrs = append(allErrs, t.validateNodePoolSchedulePlacement(field.NewPath("spec"))...)

	// Validate KEDA
	if t.Spec.KEDA != nil {
		allErrs = append(allErrs, t.validateKEDA(field.NewPath("spec.keda"))...)
		if kedaAutoscalingActive(t.Spec.KEDA) && t.Spec.MaxWorkers != nil && *t.Spec.MaxWorkers < 1 {
			allErrs = append(allErrs, field.Invalid(
				field.NewPath("spec.maxWorkers"),
				*t.Spec.MaxWorkers,
				"maxWorkers must be at least 1 when KEDA autoscaling is active"))
		}
	}

	// Validate TLS
	if t.Spec.TLS != nil {
		allErrs = append(allErrs, t.validateTLS(field.NewPath("spec.tls"))...)
	}

	// Validate Limits
	if t.Spec.Limits != nil {
		allErrs = append(allErrs, t.validateLimits(field.NewPath("spec.limits"))...)
	}

	// Validate FaultTolerantExecution
	if t.Spec.FaultTolerantExecution != nil {
		allErrs = append(allErrs, t.validateFaultTolerantExecution(field.NewPath("spec.faultTolerantExecution"))...)
	}

	// Validate OperatorNodePoolDefaults
	if t.Spec.OperatorNodePoolDefaults != nil {
		allErrs = append(allErrs, t.validateOperatorNodePoolDefaults(field.NewPath("spec.operatorNodePoolDefaults"))...)
	}

	// Validate RolloutPolicy
	if t.Spec.RolloutPolicy != nil {
		allErrs = append(allErrs, t.validateRolloutPolicy(field.NewPath("spec.rolloutPolicy"))...)
	}

	// Validate HelmChartConfig
	if t.Spec.HelmChartConfig != nil {
		allErrs = append(allErrs, t.validateHelmChartConfig(field.NewPath("spec.helmChartConfig"))...)
	}

	// Validate TrinoControlAuth
	if t.Spec.TrinoControlAuth != nil {
		allErrs = append(allErrs, t.validateTrinoControlAuth(field.NewPath("spec.trinoControlAuth"))...)
	}
	allErrs = append(allErrs, t.validateValuesOverlayPolicy(field.NewPath("spec", "valuesOverlay"))...)
	allErrs = append(allErrs, t.validateAutoscalerOwnership(field.NewPath("spec"))...)
	allErrs = append(allErrs, t.validateTrinoLifecycleAuthCompatibility(field.NewPath("spec"))...)
	allErrs = append(allErrs, t.validateTrinoLifecycleHTTPCompatibility(field.NewPath("spec"))...)
	allErrs = append(allErrs, t.validateTrinoMemoryAgainstRuntimeShape(field.NewPath("spec"))...)

	if len(allErrs) == 0 {
		return nil
	}

	return apierrors.NewInvalid(
		schema.GroupKind{Group: "analytics.xtrinode.io", Kind: "XTrinode"},
		t.Name, allErrs)
}

// updateCtx holds validation state for update operations
type updateCtx struct {
	breakGlassEnabled bool
	breakGlassReq     map[string]struct{} // dedupe reasons
	warnings          admission.Warnings
}

func newUpdateCtx(newObj *XTrinode) *updateCtx {
	return &updateCtx{
		breakGlassEnabled: hasBreakGlassAnnotation(newObj),
		breakGlassReq:     map[string]struct{}{},
	}
}

func (c *updateCtx) warn(w string) {
	c.warnings = append(c.warnings, w)
}

func (c *updateCtx) requireBreakGlass(reason string) {
	c.breakGlassReq[reason] = struct{}{}
}

func (c *updateCtx) breakGlassReasons() []string {
	out := make([]string, 0, len(c.breakGlassReq))
	for r := range c.breakGlassReq {
		out = append(out, r)
	}
	return out
}

// updateCheck is a function that validates one aspect of an update
type updateCheck func(c *updateCtx, oldObj, newObj *XTrinode)

// validateXTrinodeUpdate validates updates to a XTrinode
func (t *XTrinode) validateXTrinodeUpdate(old *XTrinode) (admission.Warnings, error) {
	// Step 1: Run base invariants (create-time validation)
	if err := t.validateXTrinode(); err != nil {
		return nil, err
	}

	c := newUpdateCtx(t)

	// Step 2: Run all update checks
	checks := []updateCheck{
		checkSizeChange,
		checkWorkerScaling,
		checkSuspended,
		checkNodePoolChanges,
		checkRoutingChanges,
		checkCatalogSelector,
		checkResourceGroupsProfile,
		checkCustomConfigMaps,
		checkLimits,
		checkValuesOverlay,
		checkFaultTolerantExecution,
		checkKEDAChanges,
		checkTLSChanges,
		checkHelmChartConfigChanges,
		checkOperatorNodePoolDefaults,
	}

	for _, chk := range checks {
		chk(c, old, t)
	}

	// Step 3: Aggregate decision
	reasons := c.breakGlassReasons()
	if len(reasons) > 0 && !c.breakGlassEnabled {
		return c.warnings, apierrors.NewInvalid(
			schema.GroupKind{Group: "analytics.xtrinode.io", Kind: "XTrinode"},
			t.Name,
			field.ErrorList{
				field.Forbidden(
					field.NewPath("spec"),
					fmt.Sprintf(
						"the following changes require break-glass annotation (%s=\"true\"): %s",
						AnnotationAllowBreakingUpdate,
						strings.Join(reasons, ", "),
					),
				),
			},
		)
	}

	if c.breakGlassEnabled && len(reasons) == 0 {
		c.warn(buildBreakGlassNotNeededWarning())
	}

	for _, warning := range nodePoolPlacementWarnings(t) {
		c.warn(warning)
	}
	for _, warning := range nodePoolFitWarnings(t) {
		c.warn(warning)
	}

	return c.warnings, nil
}

// validateRouting validates routing configuration
func (t *XTrinode) validateRouting(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	r := t.Spec.Routing

	// Routing must provide at least one selector
	hasSelector := r.Header != "" || r.Hostname != "" || r.HostnameDomain != "" || r.Default
	if !hasSelector && r.CapacityUnits == nil {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			r,
			"routing must specify at least one selector: header, hostname, hostnameDomain, or default=true"))
	}

	// Hostname and HostnameDomain should not both be set (hostname overrides auto-generation)
	if r.Hostname != "" && r.HostnameDomain != "" {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			r,
			"hostname and hostnameDomain should not both be set (hostname overrides auto-generation)"))
	}

	if r.CapacityUnits != nil && *r.CapacityUnits < 1 {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("capacityUnits"),
			*r.CapacityUnits,
			"capacityUnits must be at least 1"))
	}

	return allErrs
}

func (t *XTrinode) validateRuntimeResources(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	resources := t.Spec.Resources
	if resources.Coordinator != nil {
		allErrs = append(allErrs, validateResourceRequirements(resources.Coordinator, fldPath.Child("coordinator"))...)
	}
	if resources.Worker != nil {
		allErrs = append(allErrs, validateResourceRequirements(resources.Worker, fldPath.Child("worker"))...)
	}
	return allErrs
}

func validateResourceRequirements(requirements *corev1.ResourceRequirements, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if requirements == nil {
		return allErrs
	}
	for _, resourceName := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		request, hasRequest := requirements.Requests[resourceName]
		limit, hasLimit := requirements.Limits[resourceName]
		if hasRequest && request.Sign() < 0 {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("requests").Key(string(resourceName)),
				request.String(),
				"resource request must be non-negative"))
		}
		if hasLimit && limit.Sign() < 0 {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("limits").Key(string(resourceName)),
				limit.String(),
				"resource limit must be non-negative"))
		}
		if hasRequest && hasLimit && limit.Cmp(request) < 0 {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("limits").Key(string(resourceName)),
				limit.String(),
				fmt.Sprintf("resource limit must be greater than or equal to request %s", request.String())))
		}
	}
	return allErrs
}

func (t *XTrinode) validatePlacement(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	placement := t.Spec.Placement
	if placement.ExistingNodePool != nil {
		np := placement.ExistingNodePool
		if strings.TrimSpace(np.Provider) == "" {
			allErrs = append(allErrs, field.Required(fldPath.Child("existingNodePool", "provider"), "provider is required"))
		} else if _, _, ok := config.ExistingNodePoolSelector(np.Provider, "placeholder"); !ok {
			allErrs = append(allErrs, field.NotSupported(
				fldPath.Child("existingNodePool", "provider"),
				np.Provider,
				[]string{"azure", "aws", "gcp"}))
		}
		if strings.TrimSpace(np.Name) == "" {
			allErrs = append(allErrs, field.Required(fldPath.Child("existingNodePool", "name"), "name is required"))
		}
		if key, value, ok := config.ExistingNodePoolSelector(np.Provider, np.Name); ok {
			allErrs = append(allErrs, validateExistingNodePoolSelectorConflict(placement.NodeSelector, key, value, fldPath.Child("nodeSelector"))...)
			if placement.Coordinator != nil {
				allErrs = append(allErrs, validateExistingNodePoolSelectorConflict(placement.Coordinator.NodeSelector, key, value, fldPath.Child("coordinator", "nodeSelector"))...)
			}
			if placement.Worker != nil {
				allErrs = append(allErrs, validateExistingNodePoolSelectorConflict(placement.Worker.NodeSelector, key, value, fldPath.Child("worker", "nodeSelector"))...)
			}
		}
	}
	if placement.Coordinator != nil {
		allErrs = append(allErrs, validateRolePlacement(placement.Coordinator, fldPath.Child("coordinator"))...)
	}
	if placement.Worker != nil {
		allErrs = append(allErrs, validateRolePlacement(placement.Worker, fldPath.Child("worker"))...)
	}
	return allErrs
}

func validateRolePlacement(placement *RolePlacementSpec, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if placement == nil {
		return allErrs
	}
	for i, constraint := range placement.TopologySpreadConstraints {
		if constraint.TopologyKey == "" {
			allErrs = append(allErrs, field.Required(
				fldPath.Child("topologySpreadConstraints").Index(i).Child("topologyKey"),
				"topologyKey is required"))
		}
	}
	return allErrs
}

// validateKEDA validates KEDA configuration
func (t *XTrinode) validateKEDA(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	k := t.Spec.KEDA

	// Validate scalerType
	if k.ScalerType != "" && !validScalerTypes[normalizeString(k.ScalerType)] {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("scalerType"),
			k.ScalerType,
			"scalerType must be one of: prometheus, http"))
	}

	// Validate scalingMetric
	if k.ScalingMetric != "" && !validScalingMetrics[normalizeString(k.ScalingMetric)] {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("scalingMetric"),
			k.ScalingMetric,
			"scalingMetric must be one of: query, memory, cpu"))
	}

	scalerType := normalizeString(k.ScalerType)
	if k.HTTPEndpoint != nil {
		httpEndpoint := normalizeString(*k.HTTPEndpoint)

		// Empty scalerType defaults to HTTP when only HTTP fields are present.
		if (scalerType == "" || scalerType == "http") && httpEndpoint == "jmx" {
			if k.JMXExporter == nil || !k.JMXExporter.Enabled {
				allErrs = append(allErrs, field.Invalid(
					fldPath,
					k,
					"when httpEndpoint is jmx, jmxExporter.enabled must be true"))
			}
		}

		if httpEndpoint == "aggregator" {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("httpEndpoint"),
				*k.HTTPEndpoint,
				"httpEndpoint aggregator is no longer supported; use prometheusQuery for custom metrics or a concrete custom URL"))
		}
	}

	return allErrs
}

func (t *XTrinode) validateValuesOverlayPolicy(fldPath *field.Path) field.ErrorList {
	values := t.Spec.GetValuesOverlayMap()
	if len(values) == 0 {
		return nil
	}

	var allErrs field.ErrorList
	allErrs = append(allErrs, forbidValuesOverlayKey(values, fldPath, "resources", "use spec.resources.coordinator or spec.resources.worker for pod resources")...)
	for _, key := range []string{"nodeSelector", "tolerations", "affinity", "topologySpreadConstraints"} {
		allErrs = append(allErrs, forbidValuesOverlayKey(values, fldPath, key, "use spec.placement for scheduler constraints")...)
	}
	for _, key := range []string{"hostNetwork", "hostPID", "hostIPC"} {
		allErrs = append(allErrs, forbidValuesOverlayKey(values, fldPath, key, "host namespace settings are not allowed through valuesOverlay")...)
	}
	allErrs = append(allErrs, forbidValuesOverlayKey(values, fldPath, "sidecarContainers", "sidecar containers are not allowed through valuesOverlay by default")...)
	allErrs = append(allErrs, forbidValuesOverlayKey(values, fldPath, "envFrom", "use spec.helmChartConfig.envFrom with privileged admission instead of valuesOverlay.envFrom")...)

	if service, ok := values["service"].(map[string]interface{}); ok {
		if serviceType, ok := service["type"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(serviceType)) {
			case "loadbalancer", "nodeport":
				allErrs = append(allErrs, field.Forbidden(
					fldPath.Child("service", "type"),
					"externally exposed service types are not allowed through valuesOverlay",
				))
			}
		}
	}

	if containerSecurityContext, ok := values["containerSecurityContext"].(map[string]interface{}); ok {
		allErrs = append(allErrs, validateValuesOverlayContainerSecurityContext(containerSecurityContext, fldPath.Child("containerSecurityContext"))...)
	}

	for _, role := range []string{"coordinator", "worker"} {
		roleMap, ok := values[role].(map[string]interface{})
		if !ok {
			continue
		}
		allErrs = append(allErrs, forbidValuesOverlayKey(roleMap, fldPath.Child(role), "resources", fmt.Sprintf("use spec.resources.%s for pod resources", role))...)
		for _, key := range []string{"nodeSelector", "tolerations", "affinity", "topologySpreadConstraints"} {
			allErrs = append(allErrs, forbidValuesOverlayKey(roleMap, fldPath.Child(role), key, "use spec.placement for scheduler constraints")...)
		}
		if deployment, ok := roleMap["deployment"].(map[string]interface{}); ok {
			allErrs = append(allErrs, forbidValuesOverlayKey(deployment, fldPath.Child(role, "deployment"), "strategy", "use spec.rolloutPolicy.rollingUpdateStrategy for rollout strategy")...)
			allErrs = append(allErrs, forbidValuesOverlayKey(deployment, fldPath.Child(role, "deployment"), "revisionHistoryLimit", "use spec.rolloutPolicy.revisionHistoryLimit")...)
		}
		allErrs = append(allErrs, validateValuesOverlayAdditionalVolumes(roleMap, fldPath.Child(role))...)
	}

	return allErrs
}

func forbidValuesOverlayKey(values map[string]interface{}, fldPath *field.Path, key, message string) field.ErrorList {
	if _, ok := values[key]; !ok {
		return nil
	}
	return field.ErrorList{field.Forbidden(fldPath.Child(key), message)}
}

func validateValuesOverlayContainerSecurityContext(securityContext map[string]interface{}, fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	if boolValue(securityContext["privileged"]) {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("privileged"), "privileged containers are not allowed through valuesOverlay"))
	}
	if boolValue(securityContext["allowPrivilegeEscalation"]) {
		allErrs = append(allErrs, field.Forbidden(fldPath.Child("allowPrivilegeEscalation"), "privilege escalation is not allowed through valuesOverlay"))
	}
	if capabilities, ok := securityContext["capabilities"].(map[string]interface{}); ok {
		if adds, ok := capabilities["add"].([]interface{}); ok && len(adds) > 0 {
			allErrs = append(allErrs, field.Forbidden(fldPath.Child("capabilities", "add"), "added Linux capabilities are not allowed through valuesOverlay"))
		}
	}
	return allErrs
}

func validateValuesOverlayAdditionalVolumes(roleMap map[string]interface{}, fldPath *field.Path) field.ErrorList {
	volumes, ok := roleMap["additionalVolumes"].([]interface{})
	if !ok {
		return nil
	}
	var allErrs field.ErrorList
	for i, volume := range volumes {
		volumeMap, ok := volume.(map[string]interface{})
		if !ok {
			continue
		}
		if _, ok := volumeMap["hostPath"]; ok {
			allErrs = append(allErrs, field.Forbidden(
				fldPath.Child("additionalVolumes").Index(i).Child("hostPath"),
				"hostPath volumes are not allowed through valuesOverlay",
			))
		}
	}
	return allErrs
}

func boolValue(value interface{}) bool {
	typed, ok := value.(bool)
	return ok && typed
}

func (t *XTrinode) validateAutoscalerOwnership(fldPath *field.Path) field.ErrorList {
	if !kedaAutoscalingActive(t.Spec.KEDA) || !nativeHPAEnabled(t.Spec.GetValuesOverlayMap()) {
		return nil
	}
	return field.ErrorList{
		field.Forbidden(
			fldPath.Child("valuesOverlay").Child("server").Child("autoscaling").Child("enabled"),
			"native HPA and spec.keda cannot both manage worker replicas; choose one autoscaler"),
	}
}

func kedaAutoscalingActive(k *KEDASpec) bool {
	if k == nil || k.Enabled == nil || !*k.Enabled {
		return false
	}
	return k.ScalerType != "" ||
		k.ScalingMetric != "" ||
		(k.PrometheusServer != nil && strings.TrimSpace(*k.PrometheusServer) != "") ||
		(k.PrometheusQuery != nil && strings.TrimSpace(*k.PrometheusQuery) != "") ||
		(k.HTTPEndpoint != nil && strings.TrimSpace(*k.HTTPEndpoint) != "")
}

func nativeHPAEnabled(valuesMap map[string]interface{}) bool {
	if valuesMap == nil {
		return false
	}
	server, ok := valuesMap["server"].(map[string]interface{})
	if !ok {
		return false
	}
	autoscaling, ok := server["autoscaling"].(map[string]interface{})
	if !ok {
		return false
	}
	enabled, ok := autoscaling["enabled"].(bool)
	return ok && enabled
}

// validateTLS validates TLS configuration
func (t *XTrinode) validateTLS(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	tls := t.Spec.TLS

	// Recommended: both serverSecretClass and internalSecretClass should be set or both empty
	hasServer := tls.ServerSecretClass != ""
	hasInternal := tls.InternalSecretClass != ""

	if hasServer != hasInternal {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			tls,
			"serverSecretClass and internalSecretClass should both be set or both be empty"))
	}
	if hasServer {
		allErrs = append(allErrs, field.Forbidden(
			fldPath.Child("serverSecretClass"),
			"Trino TLS server mode disables HTTP, but XTrinode gateway routing and lifecycle control currently use HTTP-only coordinator URLs; configure TLS termination outside Trino until HTTPS control endpoint support is implemented"))
	}

	return allErrs
}

// validateLimits validates limits configuration
func (t *XTrinode) validateLimits(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	l := t.Spec.Limits

	// Validate hardConcurrencyPerGroup
	if l.HardConcurrencyPerGroup != nil && *l.HardConcurrencyPerGroup < 1 {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("hardConcurrencyPerGroup"),
			*l.HardConcurrencyPerGroup,
			"hardConcurrencyPerGroup must be at least 1"))
	}

	// Validate maxQueuedPerGroup
	if l.MaxQueuedPerGroup != nil && *l.MaxQueuedPerGroup < 0 {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("maxQueuedPerGroup"),
			*l.MaxQueuedPerGroup,
			"maxQueuedPerGroup must be at least 0"))
	}

	// Validate session limits.
	// These are free-form passthrough strings that will be rendered into Trino configs
	// We validate they are non-empty if specified, but don't parse them here
	// as they use Trino's memory format (e.g., "10GB", "512MB")
	if l.Session != nil {
		if l.Session.MaxQueryMemory != "" {
			if strings.TrimSpace(l.Session.MaxQueryMemory) == "" {
				allErrs = append(allErrs, field.Invalid(
					fldPath.Child("session", "maxQueryMemory"),
					l.Session.MaxQueryMemory,
					"maxQueryMemory must not be empty or whitespace-only"))
			} else if _, ok := parseTrinoDataSizeBytes(l.Session.MaxQueryMemory); !ok {
				allErrs = append(allErrs, field.Invalid(
					fldPath.Child("session", "maxQueryMemory"),
					l.Session.MaxQueryMemory,
					"maxQueryMemory must be a valid Trino data size such as 4GB or 512MB"))
			}
		}
		if l.Session.MaxTotalMemoryPerNode != "" {
			if strings.TrimSpace(l.Session.MaxTotalMemoryPerNode) == "" {
				allErrs = append(allErrs, field.Invalid(
					fldPath.Child("session", "maxTotalMemoryPerNode"),
					l.Session.MaxTotalMemoryPerNode,
					"maxTotalMemoryPerNode must not be empty or whitespace-only"))
			} else if _, ok := parseTrinoDataSizeBytes(l.Session.MaxTotalMemoryPerNode); !ok {
				allErrs = append(allErrs, field.Invalid(
					fldPath.Child("session", "maxTotalMemoryPerNode"),
					l.Session.MaxTotalMemoryPerNode,
					"maxTotalMemoryPerNode must be a valid Trino data size such as 4GB or 512MB"))
			}
		}
	}

	return allErrs
}

// validateFaultTolerantExecution validates Trino fault-tolerant execution settings.
func (t *XTrinode) validateFaultTolerantExecution(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	fte := t.Spec.FaultTolerantExecution

	if fte.RetryPolicy != "" && !validFaultTolerantRetryPolicies[normalizeString(fte.RetryPolicy)] {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("retryPolicy"),
			fte.RetryPolicy,
			"retryPolicy must be one of: TASK, QUERY"))
	}
	retryPolicy := normalizeString(fte.RetryPolicy)
	if retryPolicy == "" {
		retryPolicy = "task"
	}

	if fte.ExchangeManager == nil {
		return allErrs
	}

	exchangeManager := fte.ExchangeManager
	if exchangeManager.Enabled != nil && !*exchangeManager.Enabled && retryPolicy == "task" {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("exchangeManager", "enabled"),
			*exchangeManager.Enabled,
			"exchangeManager must be enabled when retryPolicy is TASK"))
	}
	if exchangeManager.Name != "" {
		allErrs = append(allErrs, validateTrinoPropertyValue(
			fldPath.Child("exchangeManager", "name"),
			exchangeManager.Name,
			"name must not be empty, whitespace-only, or contain newlines")...)
	}
	for i, dir := range exchangeManager.BaseDirectories {
		allErrs = append(allErrs, validateTrinoPropertyValue(
			fldPath.Child("exchangeManager", "baseDirectories").Index(i),
			dir,
			"baseDirectories entries must not be empty, whitespace-only, or contain newlines")...)
		if strings.Contains(dir, ",") {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("exchangeManager", "baseDirectories").Index(i),
				dir,
				"baseDirectories entries must not contain commas"))
		}
	}
	for key, value := range exchangeManager.Properties {
		keyPath := fldPath.Child("exchangeManager", "properties").Key(key)
		if key == "exchange-manager.name" || key == "exchange.base-directories" {
			allErrs = append(allErrs, field.Forbidden(
				keyPath,
				"use exchangeManager.name or exchangeManager.baseDirectories for this property"))
			continue
		}
		allErrs = append(allErrs, validateTrinoPropertyKey(keyPath, key)...)
		allErrs = append(allErrs, validateTrinoPropertyValue(
			keyPath,
			value,
			"property values must not be empty, whitespace-only, or contain newlines")...)
	}

	return allErrs
}

func validateTrinoPropertyKey(fldPath *field.Path, key string) field.ErrorList {
	var allErrs field.ErrorList
	if strings.TrimSpace(key) == "" || strings.TrimSpace(key) != key || strings.ContainsAny(key, "\r\n=") {
		allErrs = append(allErrs, field.Invalid(
			fldPath,
			key,
			"property keys must not be empty, have surrounding whitespace, or contain newlines or '='"))
	}
	return allErrs
}

func validateTrinoPropertyValue(fldPath *field.Path, value, message string) field.ErrorList {
	var allErrs field.ErrorList
	if strings.TrimSpace(value) == "" || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n") {
		allErrs = append(allErrs, field.Invalid(fldPath, value, message))
	}
	return allErrs
}

// validateOperatorNodePoolDefaults validates operator nodePool defaults
func (t *XTrinode) validateOperatorNodePoolDefaults(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	d := t.Spec.OperatorNodePoolDefaults

	// Validate defaultMinNodes <= defaultMaxNodes
	if d.DefaultMinNodes != nil && d.DefaultMaxNodes != nil {
		if *d.DefaultMinNodes > *d.DefaultMaxNodes {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("defaultMinNodes"),
				*d.DefaultMinNodes,
				"defaultMinNodes must be less than or equal to defaultMaxNodes"))
		}
	}

	return allErrs
}

// validateRolloutPolicy validates rollout policy configuration
func (t *XTrinode) validateRolloutPolicy(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	rp := t.Spec.RolloutPolicy

	// Validate revisionHistoryLimit range
	if rp.RevisionHistoryLimit != nil {
		if *rp.RevisionHistoryLimit < 0 || *rp.RevisionHistoryLimit > 100 {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("revisionHistoryLimit"),
				*rp.RevisionHistoryLimit,
				"revisionHistoryLimit must be between 0 and 100"))
		}
	}

	if rp.RollingUpdateStrategy != nil {
		strategyPath := fldPath.Child("rollingUpdateStrategy")
		strategy := rp.RollingUpdateStrategy
		allErrs = append(allErrs, validateIntOrPercent(strategyPath.Child("maxSurge"), strategy.MaxSurge)...)
		allErrs = append(allErrs, validateIntOrPercent(strategyPath.Child("maxUnavailable"), strategy.MaxUnavailable)...)

		if intOrPercentIsExplicitZero(strategy.MaxSurge) && intOrPercentIsExplicitZero(strategy.MaxUnavailable) {
			allErrs = append(allErrs, field.Invalid(
				strategyPath,
				strategy,
				"maxSurge and maxUnavailable cannot both be zero"))
		}
	}

	return allErrs
}

func validateIntOrPercent(fldPath *field.Path, value *intstr.IntOrString) field.ErrorList {
	var allErrs field.ErrorList
	if value == nil {
		return allErrs
	}

	switch value.Type {
	case intstr.Int:
		if value.IntVal < 0 {
			allErrs = append(allErrs, field.Invalid(fldPath, value.IntVal, "must be a non-negative integer"))
		}
	case intstr.String:
		strValue := strings.TrimSpace(value.StrVal)
		if strings.HasSuffix(strValue, "%") {
			parsed, err := strconv.Atoi(strings.TrimSuffix(strValue, "%"))
			if err != nil || parsed < 0 || parsed > 100 {
				allErrs = append(allErrs, field.Invalid(fldPath, value.StrVal, "percentage must be between 0% and 100%"))
			}
			return allErrs
		}

		parsed, err := strconv.Atoi(strValue)
		if err != nil || parsed < 0 {
			allErrs = append(allErrs, field.Invalid(fldPath, value.StrVal, "must be a non-negative integer or percentage string"))
		}
	default:
		allErrs = append(allErrs, field.Invalid(fldPath, value, "must be an integer or percentage string"))
	}

	return allErrs
}

func intOrPercentIsExplicitZero(value *intstr.IntOrString) bool {
	if value == nil {
		return false
	}
	if value.Type == intstr.Int {
		return value.IntVal == 0
	}
	if value.Type == intstr.String {
		strValue := strings.TrimSpace(value.StrVal)
		return strValue == "0" || strValue == "0%"
	}
	return false
}

// validateHelmChartConfig validates Helm chart configuration
func (t *XTrinode) validateHelmChartConfig(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	hcc := t.Spec.HelmChartConfig

	// Validate access control
	if hcc.AccessControl != nil {
		ac := hcc.AccessControl
		if !validAccessControlTypes[normalizeString(ac.Type)] {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("accessControl", "type"),
				ac.Type,
				"accessControl.type must be one of: configmap, properties"))
		}

		// If type is configmap, require configFile
		if normalizeString(ac.Type) == "configmap" {
			if ac.ConfigFile == "" {
				allErrs = append(allErrs, field.Required(
					fldPath.Child("accessControl", "configFile"),
					"configFile is required when type is configmap"))
			}
		}
	}

	// Validate ingress
	if hcc.Ingress != nil && hcc.Ingress.Enabled {
		if len(hcc.Ingress.Hosts) == 0 {
			allErrs = append(allErrs, field.Required(
				fldPath.Child("ingress", "hosts"),
				"hosts is required when ingress is enabled"))
		}
		for i, host := range hcc.Ingress.Hosts {
			if host.Host == "" {
				allErrs = append(allErrs, field.Required(
					fldPath.Child("ingress", "hosts").Index(i).Child("host"),
					"host is required"))
			}
		}
	}

	// Validate worker graceful shutdown
	if hcc.Worker != nil && hcc.Worker.GracefulShutdown != nil && hcc.Worker.GracefulShutdown.Enabled {
		if hcc.Worker.GracefulShutdown.GracePeriodSeconds <= 0 {
			allErrs = append(allErrs, field.Invalid(
				fldPath.Child("worker", "gracefulShutdown", "gracePeriodSeconds"),
				hcc.Worker.GracefulShutdown.GracePeriodSeconds,
				"gracePeriodSeconds must be greater than 0 when graceful shutdown is enabled"))
		}
	}

	return allErrs
}

func (t *XTrinode) validateTrinoControlAuth(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	auth := t.Spec.TrinoControlAuth

	if strings.Contains(auth.Username, ":") {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("username"),
			auth.Username,
			"username must not contain ':'",
		))
	}
	if strings.TrimSpace(auth.Username) != auth.Username {
		allErrs = append(allErrs, field.Invalid(
			fldPath.Child("username"),
			auth.Username,
			"username must not have leading or trailing whitespace",
		))
	}

	if auth.PasswordSecret == nil {
		allErrs = append(allErrs, field.Required(
			fldPath.Child("passwordSecret"),
			"passwordSecret is required when trinoControlAuth is set",
		))
		return allErrs
	}
	if strings.TrimSpace(auth.PasswordSecret.Name) == "" {
		allErrs = append(allErrs, field.Required(
			fldPath.Child("passwordSecret", "name"),
			"Secret name must be set",
		))
	}
	if strings.TrimSpace(auth.PasswordSecret.Key) == "" {
		allErrs = append(allErrs, field.Required(
			fldPath.Child("passwordSecret", "key"),
			"Secret key must be set",
		))
	}

	return allErrs
}

func (t *XTrinode) validateTrinoLifecycleAuthCompatibility(fldPath *field.Path) field.ErrorList {
	authSettings := valuesOverlayAuthenticationSettings(t.Spec.GetValuesOverlayMap(), fldPath)
	if len(authSettings) == 0 {
		return nil
	}

	var allErrs field.ErrorList
	hasAuth := false
	hasPassword := false

	for _, setting := range authSettings {
		authTypes := splitTrinoAuthenticationTypes(setting.value)
		if len(authTypes) == 0 {
			continue
		}
		hasAuth = true
		for _, typ := range authTypes {
			if typ == "PASSWORD" {
				hasPassword = true
				continue
			}
			allErrs = append(allErrs, field.NotSupported(
				setting.path,
				typ,
				[]string{"PASSWORD"},
			))
		}
	}
	if !hasAuth {
		return allErrs
	}
	if t.Spec.TrinoControlAuth == nil || t.Spec.TrinoControlAuth.PasswordSecret == nil {
		allErrs = append(allErrs, field.Required(
			fldPath.Child("trinoControlAuth"),
			"trinoControlAuth with passwordSecret is required when Trino HTTP authentication is enabled",
		))
	}
	if hasPassword && !valuesOverlayHasConfigProperty(t.Spec.GetValuesOverlayMap(), "internal-communication.shared-secret") {
		allErrs = append(allErrs, field.Required(
			fldPath.Child("valuesOverlay", "additionalConfigProperties"),
			"internal-communication.shared-secret is required when Trino HTTP authentication is enabled",
		))
	}

	return allErrs
}

func (t *XTrinode) validateTrinoLifecycleHTTPCompatibility(fldPath *field.Path) field.ErrorList {
	var allErrs field.ErrorList
	for _, setting := range valuesOverlayConfigPropertySettings(t.Spec.GetValuesOverlayMap(), "http-server.http.enabled", fldPath) {
		if strings.EqualFold(strings.TrimSpace(setting.value), "false") {
			allErrs = append(allErrs, field.Forbidden(
				setting.path,
				"Trino HTTP listener must stay enabled because XTrinode gateway routing and lifecycle control currently use HTTP coordinator service URLs",
			))
		}
	}
	for _, setting := range valuesOverlayConfigPropertySettings(t.Spec.GetValuesOverlayMap(), "http-server.http.port", fldPath) {
		allErrs = append(allErrs, field.Forbidden(
			setting.path,
			"do not override http-server.http.port directly; configure valuesOverlay.service.port so generated Services, status URLs, and lifecycle control use the same HTTP port",
		))
	}
	return allErrs
}

func (t *XTrinode) validateTrinoMemoryAgainstRuntimeShape(fldPath *field.Path) field.ErrorList {
	workerLimit, ok := t.workerMemoryLimitBytes()
	if !ok || workerLimit <= 0 {
		return nil
	}

	var allErrs field.ErrorList
	if t.Spec.Limits != nil && t.Spec.Limits.Session != nil && t.Spec.Limits.Session.MaxTotalMemoryPerNode != "" {
		allErrs = append(allErrs, validateTrinoDataSizeAtMost(
			fldPath.Child("limits", "session", "maxTotalMemoryPerNode"),
			t.Spec.Limits.Session.MaxTotalMemoryPerNode,
			workerLimit,
			"maxTotalMemoryPerNode must not exceed resolved worker memory limit",
		)...)
	}

	for _, key := range []string{"query.max-memory-per-node", "query.max-total-memory-per-node"} {
		for _, setting := range valuesOverlayConfigPropertySettings(t.Spec.GetValuesOverlayMap(), key, fldPath) {
			allErrs = append(allErrs, validateTrinoDataSizeAtMost(
				setting.path,
				setting.value,
				workerLimit,
				fmt.Sprintf("%s must not exceed resolved worker memory limit", key),
			)...)
		}
	}

	return allErrs
}

func (t *XTrinode) workerMemoryLimitBytes() (int64, bool) {
	resources, _, ok := t.resolvedWorkerResourcesForMachineRecommendation()
	if !ok {
		return 0, false
	}
	if limit, ok := resources.Limits[corev1.ResourceMemory]; ok && limit.Sign() > 0 {
		return limit.Value(), true
	}
	if request, ok := resources.Requests[corev1.ResourceMemory]; ok && request.Sign() > 0 {
		return request.Value(), true
	}
	return 0, false
}

func validateTrinoDataSizeAtMost(fldPath *field.Path, value string, maxBytes int64, message string) field.ErrorList {
	parsed, ok := parseTrinoDataSizeBytes(value)
	if !ok {
		return field.ErrorList{
			field.Invalid(fldPath, value, "must be a valid Trino data size such as 4GB or 512MB"),
		}
	}
	if parsed > maxBytes {
		return field.ErrorList{
			field.Invalid(fldPath, value, message),
		}
	}
	return nil
}

func parseTrinoDataSizeBytes(value string) (int64, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0, false
	}
	upper := strings.ToUpper(trimmed)
	units := []struct {
		suffix     string
		multiplier float64
	}{
		{suffix: "TIB", multiplier: 1024 * 1024 * 1024 * 1024},
		{suffix: "TB", multiplier: 1000 * 1000 * 1000 * 1000},
		{suffix: "GIB", multiplier: 1024 * 1024 * 1024},
		{suffix: "GB", multiplier: 1000 * 1000 * 1000},
		{suffix: "MIB", multiplier: 1024 * 1024},
		{suffix: "MB", multiplier: 1000 * 1000},
		{suffix: "KIB", multiplier: 1024},
		{suffix: "KB", multiplier: 1000},
		{suffix: "B", multiplier: 1},
	}
	for _, unit := range units {
		if !strings.HasSuffix(upper, unit.suffix) {
			continue
		}
		number := strings.TrimSpace(trimmed[:len(trimmed)-len(unit.suffix)])
		if number == "" {
			return 0, false
		}
		parsed, err := strconv.ParseFloat(number, 64)
		if err != nil || parsed < 0 {
			return 0, false
		}
		return int64(parsed * unit.multiplier), true
	}

	quantity, err := resource.ParseQuantity(trimmed)
	if err != nil || quantity.Sign() < 0 {
		return 0, false
	}
	return quantity.Value(), true
}

func valuesOverlayHasConfigProperty(values map[string]interface{}, key string) bool {
	if values == nil {
		return false
	}
	props, ok := values["additionalConfigProperties"].([]interface{})
	if !ok {
		return false
	}
	for _, prop := range props {
		propStr, ok := prop.(string)
		if !ok {
			continue
		}
		name, value, found := strings.Cut(propStr, "=")
		if !found {
			continue
		}
		if strings.TrimSpace(name) == key && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

type valuesOverlayConfigSetting struct {
	value string
	path  *field.Path
}

func valuesOverlayAuthenticationSettings(values map[string]interface{}, fldPath *field.Path) []valuesOverlayConfigSetting {
	if values == nil {
		return nil
	}
	var settings []valuesOverlayConfigSetting
	server, ok := values["server"].(map[string]interface{})
	if ok {
		if cfg, ok := server["config"].(map[string]interface{}); ok {
			if authType, ok := cfg["authenticationType"].(string); ok {
				settings = append(settings, valuesOverlayConfigSetting{
					value: authType,
					path:  fldPath.Child("valuesOverlay", "server", "config", "authenticationType"),
				})
			}
		}
	}
	settings = append(settings, valuesOverlayConfigPropertySettings(values, "http-server.authentication.type", fldPath)...)
	return settings
}

func valuesOverlayConfigPropertySettings(values map[string]interface{}, key string, fldPath *field.Path) []valuesOverlayConfigSetting {
	if values == nil {
		return nil
	}
	var settings []valuesOverlayConfigSetting
	if props, ok := values["additionalConfigProperties"].([]interface{}); ok {
		for _, prop := range props {
			propStr, ok := prop.(string)
			if !ok {
				continue
			}
			if value, found := configPropertyValue(propStr, key); found {
				settings = append(settings, valuesOverlayConfigSetting{
					value: value,
					path:  fldPath.Child("valuesOverlay", "additionalConfigProperties"),
				})
			}
		}
	}

	if server, ok := values["server"].(map[string]interface{}); ok {
		for _, extraConfigField := range []string{"coordinatorExtraConfig", "workerExtraConfig"} {
			extraConfig, ok := server[extraConfigField].(string)
			if !ok {
				continue
			}
			for _, value := range configPropertyValuesFromText(extraConfig, key) {
				settings = append(settings, valuesOverlayConfigSetting{
					value: value,
					path:  fldPath.Child("valuesOverlay", "server", extraConfigField),
				})
			}
		}
	}
	return settings
}

func configPropertyValuesFromText(text, key string) []string {
	var values []string
	for _, line := range strings.Split(text, "\n") {
		if value, found := configPropertyValue(line, key); found {
			values = append(values, value)
		}
	}
	return values
}

func configPropertyValue(line, key string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", false
	}
	name, value, found := strings.Cut(line, "=")
	if !found {
		return "", false
	}
	if strings.TrimSpace(name) != key {
		return "", false
	}
	return strings.TrimSpace(value), true
}

func splitTrinoAuthenticationTypes(authType string) []string {
	parts := strings.FieldsFunc(authType, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		normalized := strings.ToUpper(strings.TrimSpace(part))
		if normalized != "" {
			out = append(out, normalized)
		}
	}
	return out
}

// Helper functions
