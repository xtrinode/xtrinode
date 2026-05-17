package status

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/events"
)

func TestSetConditionWithEvents_ReadyTransition(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{},
		},
	}

	fakeRecorder := record.NewFakeRecorder(10)
	eventRecorder := events.NewRecorder(fakeRecorder, events.DefaultConfig())

	// Set Ready to True (first time)
	SetConditionWithEvents(xtrinode, ConditionTypeReady, metav1.ConditionTrue, ConditionReasonAllComponentsReady, "All components ready", eventRecorder)

	// Check event was recorded
	select {
	case event := <-fakeRecorder.Events:
		assert.Contains(t, event, "Normal")
		assert.Contains(t, event, events.ReasonConditionReadyTrue)
		assert.Contains(t, event, "Ready condition became True")
		t.Logf("Ready True event recorded: %s", event)
	default:
		t.Error("Expected ConditionReadyTrue event but none was recorded")
	}

	// Set Ready to False (transition)
	SetConditionWithEvents(xtrinode, ConditionTypeReady, metav1.ConditionFalse, ConditionReasonSuspended, "XTrinode is suspended", eventRecorder)

	// Check event was recorded
	select {
	case event := <-fakeRecorder.Events:
		assert.Contains(t, event, "Warning")
		assert.Contains(t, event, events.ReasonConditionReadyFalse)
		assert.Contains(t, event, "Ready condition became False")
		t.Logf("Ready False event recorded: %s", event)
	default:
		t.Error("Expected ConditionReadyFalse event but none was recorded")
	}

	// Set Ready to True again (transition)
	SetConditionWithEvents(xtrinode, ConditionTypeReady, metav1.ConditionTrue, ConditionReasonAllComponentsReady, "All components ready", eventRecorder)

	// Check event was recorded
	select {
	case event := <-fakeRecorder.Events:
		assert.Contains(t, event, "Normal")
		assert.Contains(t, event, events.ReasonConditionReadyTrue)
		t.Logf("Ready True event recorded again: %s", event)
	default:
		t.Error("Expected ConditionReadyTrue event but none was recorded")
	}
}

func TestSetConditionWithEvents_ErrorTransition(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{},
		},
	}

	fakeRecorder := record.NewFakeRecorder(10)
	eventRecorder := events.NewRecorder(fakeRecorder, events.DefaultConfig())

	// Set Error to True (first time)
	SetConditionWithEvents(xtrinode, ConditionTypeError, metav1.ConditionTrue, ConditionReasonResourceBuildFailed, "Failed to build resources", eventRecorder)

	// Check event was recorded
	select {
	case event := <-fakeRecorder.Events:
		assert.Contains(t, event, "Warning")
		assert.Contains(t, event, events.ReasonConditionErrorTrue)
		assert.Contains(t, event, "Error condition became True")
		t.Logf("Error True event recorded: %s", event)
	default:
		t.Error("Expected ConditionErrorTrue event but none was recorded")
	}

	// Set Error to False (transition)
	SetConditionWithEvents(xtrinode, ConditionTypeError, metav1.ConditionFalse, ConditionReasonNoError, "No errors", eventRecorder)

	// Check event was recorded
	select {
	case event := <-fakeRecorder.Events:
		assert.Contains(t, event, "Normal")
		assert.Contains(t, event, events.ReasonConditionErrorFalse)
		assert.Contains(t, event, "Error condition became False")
		t.Logf("Error False event recorded: %s", event)
	default:
		t.Error("Expected ConditionErrorFalse event but none was recorded")
	}
}

func TestSetConditionWithEvents_SuspendedTransition(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{},
		},
	}

	fakeRecorder := record.NewFakeRecorder(10)
	eventRecorder := events.NewRecorder(fakeRecorder, events.DefaultConfig())

	// Set Suspended to True (first time)
	SetConditionWithEvents(xtrinode, ConditionTypeSuspended, metav1.ConditionTrue, ConditionReasonSuspended, "XTrinode suspended", eventRecorder)

	// Check event was recorded
	select {
	case event := <-fakeRecorder.Events:
		assert.Contains(t, event, "Normal")
		assert.Contains(t, event, events.ReasonConditionSuspendedTrue)
		assert.Contains(t, event, "Suspended condition became True")
		t.Logf("Suspended True event recorded: %s", event)
	default:
		t.Error("Expected ConditionSuspendedTrue event but none was recorded")
	}

	// Set Suspended to False (transition)
	SetConditionWithEvents(xtrinode, ConditionTypeSuspended, metav1.ConditionFalse, ConditionReasonNotSuspended, "Not suspended", eventRecorder)

	// Check event was recorded
	select {
	case event := <-fakeRecorder.Events:
		assert.Contains(t, event, "Normal")
		assert.Contains(t, event, events.ReasonConditionSuspendedFalse)
		assert.Contains(t, event, "Suspended condition became False")
		t.Logf("Suspended False event recorded: %s", event)
	default:
		t.Error("Expected ConditionSuspendedFalse event but none was recorded")
	}
}

func TestSetConditionWithEvents_NoTransition(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{},
		},
	}

	fakeRecorder := record.NewFakeRecorder(10)
	eventRecorder := events.NewRecorder(fakeRecorder, events.DefaultConfig())

	// Set Ready to True
	SetConditionWithEvents(xtrinode, ConditionTypeReady, metav1.ConditionTrue, ConditionReasonAllComponentsReady, "All components ready", eventRecorder)

	// Drain the event
	<-fakeRecorder.Events

	// Set Ready to True again (same status, no transition)
	SetConditionWithEvents(xtrinode, ConditionTypeReady, metav1.ConditionTrue, ConditionReasonAllComponentsReady, "All components ready", eventRecorder)

	// No event should be recorded
	select {
	case event := <-fakeRecorder.Events:
		t.Errorf("Expected no event for same status, but got: %s", event)
	default:
		// Expected - no event recorded
	}
}

func TestSetConditionWithEvents_NilEventRecorder(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{},
		},
	}

	// Should not panic with nil event recorder
	SetConditionWithEvents(xtrinode, ConditionTypeReady, metav1.ConditionTrue, ConditionReasonAllComponentsReady, "All components ready", nil)

	// Condition should still be set
	condition := GetCondition(xtrinode, ConditionTypeReady)
	assert.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
}

func TestSetConditionWithEvents_OtherConditionType(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{},
		},
	}

	fakeRecorder := record.NewFakeRecorder(10)
	eventRecorder := events.NewRecorder(fakeRecorder, events.DefaultConfig())

	// Set Reconciling condition (not tracked for events)
	SetConditionWithEvents(xtrinode, ConditionTypeReconciling, metav1.ConditionTrue, ConditionReasonReconciling, "Reconciling", eventRecorder)

	// No event should be recorded for Reconciling condition
	select {
	case event := <-fakeRecorder.Events:
		t.Errorf("Expected no event for Reconciling condition, but got: %s", event)
	default:
		// Expected - no event recorded for Reconciling
	}

	// Condition should still be set
	condition := GetCondition(xtrinode, ConditionTypeReconciling)
	assert.NotNil(t, condition)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
}
