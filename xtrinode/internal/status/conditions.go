package status

import (
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition types
const (
	ConditionTypeReady       = "Ready"
	ConditionTypeReconciling = "Reconciling"
	ConditionTypeSuspended   = "Suspended"
	ConditionTypeError       = "Error"

	// Component-scoped conditions
	ConditionTypeGuardrailsReady     = "GuardrailsReady"
	ConditionTypeNodePoolReady       = "NodePoolReady"
	ConditionTypeTrinoResourcesReady = "TrinoResourcesReady"
	ConditionTypeKEDAReady           = "KEDAReady"
	ConditionTypeGatewayReady        = "GatewayReady"
	ConditionTypeSchedulingReady     = "SchedulingReady"
	ConditionTypePlacementReady      = "PlacementReady"
	ConditionTypeTaintsReady         = "TaintsReady"
	ConditionTypeQuotaReady          = "QuotaReady"
	ConditionTypeCapacityReady       = "CapacityReady"
	ConditionTypeNodePoolFitReady    = "NodePoolFitReady"
)

// Condition reasons
const (
	ConditionReasonAllComponentsReady  = "AllComponentsReady"
	ConditionReasonReconciling         = "Reconciling"
	ConditionReasonSuspended           = "Suspended"
	ConditionReasonResourceBuildFailed = "ResourceBuildFailed"
	ConditionReasonResourceApplyFailed = "ResourceApplyFailed"
	ConditionReasonKEDAScaleFailed     = "KEDAScaleFailed"
	ConditionReasonKEDAPlatformMissing = "KEDAPlatformMissing"
	ConditionReasonGatewayFailed       = "GatewayRegistrationFailed"
	ConditionReasonNodePoolFailed      = "NodePoolProvisioningFailed"
	ConditionReasonNodePoolFitOK       = "NodePoolFitOK"
	ConditionReasonNodePoolFitUnknown  = "NodePoolFitUnknown"
	ConditionReasonNodePoolFitFailed   = "NodePoolFitFailed"
	ConditionReasonNamespaceFailed     = "NamespaceGuardrailsFailed"
	ConditionReasonRuntimeReady        = "RuntimeReady"
	ConditionReasonRuntimeNotReady     = "RuntimeNotReady"
	ConditionReasonSchedulingReady     = "SchedulingReady"
	ConditionReasonSchedulingBlocked   = "SchedulingBlocked"
	ConditionReasonSchedulingUnknown   = "SchedulingUnknown"
	ConditionReasonPlacementReady      = "PlacementReady"
	ConditionReasonPlacementBlocked    = "PlacementBlocked"
	ConditionReasonPlacementUnknown    = "PlacementUnknown"
	ConditionReasonTaintsReady         = "TaintsReady"
	ConditionReasonTaintsBlocked       = "TaintsBlocked"
	ConditionReasonTaintsUnknown       = "TaintsUnknown"
	ConditionReasonQuotaReady          = "QuotaReady"
	ConditionReasonQuotaBlocked        = "QuotaBlocked"
	ConditionReasonQuotaUnknown        = "QuotaUnknown"
	ConditionReasonCapacityReady       = "CapacityReady"
	ConditionReasonCapacityBlocked     = "CapacityBlocked"
	ConditionReasonCapacityUnknown     = "CapacityUnknown"
	ConditionReasonSuspendFailed       = "SuspendFailed"
	ConditionReasonResumeFailed        = "ResumeFailed"
	ConditionReasonNoError             = "NoError"      // Error condition cleared
	ConditionReasonNotSuspended        = "NotSuspended" // Suspended condition cleared
)

// SetCondition sets a condition on the XTrinode status
func SetCondition(xtrinode *analyticsv1.XTrinode, conditionType string, status metav1.ConditionStatus, reason, message string) {
	if xtrinode.Status.Conditions == nil {
		xtrinode.Status.Conditions = []metav1.Condition{}
	}

	now := metav1.Now()
	condition := metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: xtrinode.Generation,
	}

	// Find existing condition
	for i, c := range xtrinode.Status.Conditions {
		if c.Type == conditionType {
			// Update existing condition if status or reason changed
			if c.Status != status || c.Reason != reason {
				xtrinode.Status.Conditions[i] = condition
			} else {
				// Update message and observed generation even if status/reason unchanged
				xtrinode.Status.Conditions[i].Message = message
				xtrinode.Status.Conditions[i].ObservedGeneration = xtrinode.Generation
			}
			return
		}
	}

	// Add new condition
	xtrinode.Status.Conditions = append(xtrinode.Status.Conditions, condition)
}

// GetCondition returns the condition with the given type
func GetCondition(xtrinode *analyticsv1.XTrinode, conditionType string) *metav1.Condition {
	if xtrinode.Status.Conditions == nil {
		return nil
	}

	for i := range xtrinode.Status.Conditions {
		if xtrinode.Status.Conditions[i].Type == conditionType {
			return &xtrinode.Status.Conditions[i]
		}
	}

	return nil
}

// IsReady returns true if the Ready condition is True
func IsReady(xtrinode *analyticsv1.XTrinode) bool {
	condition := GetCondition(xtrinode, ConditionTypeReady)
	return condition != nil && condition.Status == metav1.ConditionTrue
}

// HasError returns true if the Error condition is True
func HasError(xtrinode *analyticsv1.XTrinode) bool {
	condition := GetCondition(xtrinode, ConditionTypeError)
	return condition != nil && condition.Status == metav1.ConditionTrue
}
