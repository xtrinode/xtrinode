package status

import (
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/events"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SetConditionWithEvents sets a condition and records events for significant transitions
// This is a wrapper around SetCondition that detects condition status changes and records events
// Only records events for Ready, Error, and Suspended conditions
func SetConditionWithEvents(
	xtrinode *analyticsv1.XTrinode,
	conditionType string,
	status metav1.ConditionStatus,
	reason, message string,
	eventRecorder events.Recorder,
) {
	// Get old condition status before updating
	oldCondition := GetCondition(xtrinode, conditionType)
	oldStatus := metav1.ConditionUnknown
	if oldCondition != nil {
		oldStatus = oldCondition.Status
	}

	// Set the condition
	SetCondition(xtrinode, conditionType, status, reason, message)

	// Record events for significant transitions
	if eventRecorder == nil {
		return // No event recorder provided, skip event recording
	}

	// Only record events if status actually changed
	if oldStatus == status {
		return // No transition, skip event recording
	}

	// Record events based on condition type and transition
	switch conditionType {
	case ConditionTypeReady:
		switch status {
		case metav1.ConditionTrue:
			eventRecorder.Normal(xtrinode, events.ReasonConditionReadyTrue, events.FormatMessage("Ready condition became True: %s", message))
		case metav1.ConditionFalse:
			eventRecorder.Warning(xtrinode, events.ReasonConditionReadyFalse, events.FormatMessage("Ready condition became False: %s", message))
		}

	case ConditionTypeError:
		switch status {
		case metav1.ConditionTrue:
			eventRecorder.Warning(xtrinode, events.ReasonConditionErrorTrue, events.FormatMessage("Error condition became True: %s", message))
		case metav1.ConditionFalse:
			eventRecorder.Normal(xtrinode, events.ReasonConditionErrorFalse, events.FormatMessage("Error condition became False: %s", message))
		}

	case ConditionTypeSuspended:
		switch status {
		case metav1.ConditionTrue:
			eventRecorder.Normal(xtrinode, events.ReasonConditionSuspendedTrue, events.FormatMessage("Suspended condition became True: %s", message))
		case metav1.ConditionFalse:
			eventRecorder.Normal(xtrinode, events.ReasonConditionSuspendedFalse, events.FormatMessage("Suspended condition became False: %s", message))
		}
	}
}
