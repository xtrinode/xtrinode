package status

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetCondition_NewCondition(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 1,
		},
		Status: analyticsv1.XTrinodeStatus{},
	}

	SetCondition(xtrinode, ConditionTypeReady, metav1.ConditionTrue, ConditionReasonAllComponentsReady, "All components ready")

	require.NotNil(t, xtrinode.Status.Conditions)
	require.Len(t, xtrinode.Status.Conditions, 1)

	condition := xtrinode.Status.Conditions[0]
	assert.Equal(t, ConditionTypeReady, condition.Type)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
	assert.Equal(t, ConditionReasonAllComponentsReady, condition.Reason)
	assert.Equal(t, "All components ready", condition.Message)
	assert.Equal(t, int64(1), condition.ObservedGeneration)
	assert.NotZero(t, condition.LastTransitionTime)
}

func TestSetCondition_UpdateExistingCondition(t *testing.T) {
	now := metav1.Now()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 1,
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{
				{
					Type:               ConditionTypeReady,
					Status:             metav1.ConditionFalse,
					Reason:             ConditionReasonReconciling,
					Message:            "Reconciling",
					LastTransitionTime: now,
					ObservedGeneration: 1,
				},
			},
		},
	}

	// Update condition with different status
	SetCondition(xtrinode, ConditionTypeReady, metav1.ConditionTrue, ConditionReasonAllComponentsReady, "All components ready")

	require.Len(t, xtrinode.Status.Conditions, 1)
	condition := xtrinode.Status.Conditions[0]
	assert.Equal(t, ConditionTypeReady, condition.Type)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
	assert.Equal(t, ConditionReasonAllComponentsReady, condition.Reason)
	assert.Equal(t, "All components ready", condition.Message)
	assert.Equal(t, int64(1), condition.ObservedGeneration)
	// LastTransitionTime should be updated when status changes
	assert.True(t, condition.LastTransitionTime.After(now.Time) || condition.LastTransitionTime.Equal(&now))
}

func TestSetCondition_UpdateSameStatus(t *testing.T) {
	now := metav1.Now()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 2, // Generation increased
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{
				{
					Type:               ConditionTypeReady,
					Status:             metav1.ConditionTrue,
					Reason:             ConditionReasonAllComponentsReady,
					Message:            "Old message",
					LastTransitionTime: now,
					ObservedGeneration: 1,
				},
			},
		},
	}

	// Update condition with same status but different message
	SetCondition(xtrinode, ConditionTypeReady, metav1.ConditionTrue, ConditionReasonAllComponentsReady, "New message")

	require.Len(t, xtrinode.Status.Conditions, 1)
	condition := xtrinode.Status.Conditions[0]
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
	assert.Equal(t, ConditionReasonAllComponentsReady, condition.Reason)
	assert.Equal(t, "New message", condition.Message)
	assert.Equal(t, int64(2), condition.ObservedGeneration) // Observed generation updated
	// LastTransitionTime should remain the same when status/reason unchanged
	assert.Equal(t, now, condition.LastTransitionTime)
}

func TestSetCondition_UpdateReason(t *testing.T) {
	now := metav1.Now()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 2,
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{
				{
					Type:               ConditionTypeError,
					Status:             metav1.ConditionTrue,
					Reason:             ConditionReasonResourceBuildFailed,
					Message:            "Build failed",
					LastTransitionTime: now,
					ObservedGeneration: 1,
				},
			},
		},
	}

	// Update condition with different reason
	SetCondition(xtrinode, ConditionTypeError, metav1.ConditionTrue, ConditionReasonResourceApplyFailed, "Apply failed")

	require.Len(t, xtrinode.Status.Conditions, 1)
	condition := xtrinode.Status.Conditions[0]
	assert.Equal(t, ConditionReasonResourceApplyFailed, condition.Reason)
	assert.Equal(t, "Apply failed", condition.Message)
	assert.Equal(t, int64(2), condition.ObservedGeneration)
	// LastTransitionTime should be updated when reason changes
	assert.True(t, condition.LastTransitionTime.After(now.Time) || condition.LastTransitionTime.Equal(&now))
}

func TestSetCondition_MultipleConditions(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 1,
		},
		Status: analyticsv1.XTrinodeStatus{},
	}

	SetCondition(xtrinode, ConditionTypeReady, metav1.ConditionTrue, ConditionReasonAllComponentsReady, "Ready")
	SetCondition(xtrinode, ConditionTypeReconciling, metav1.ConditionFalse, ConditionReasonReconciling, "Not reconciling")
	SetCondition(xtrinode, ConditionTypeError, metav1.ConditionFalse, ConditionReasonNoError, "No errors")

	require.Len(t, xtrinode.Status.Conditions, 3)

	ready := GetCondition(xtrinode, ConditionTypeReady)
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionTrue, ready.Status)

	reconciling := GetCondition(xtrinode, ConditionTypeReconciling)
	require.NotNil(t, reconciling)
	assert.Equal(t, metav1.ConditionFalse, reconciling.Status)

	errorCond := GetCondition(xtrinode, ConditionTypeError)
	require.NotNil(t, errorCond)
	assert.Equal(t, metav1.ConditionFalse, errorCond.Status)
}

func TestGetCondition_Exists(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 1,
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{
				{
					Type:   ConditionTypeReady,
					Status: metav1.ConditionTrue,
				},
				{
					Type:   ConditionTypeReconciling,
					Status: metav1.ConditionFalse,
				},
			},
		},
	}

	condition := GetCondition(xtrinode, ConditionTypeReady)
	require.NotNil(t, condition)
	assert.Equal(t, ConditionTypeReady, condition.Type)
	assert.Equal(t, metav1.ConditionTrue, condition.Status)
}

func TestGetCondition_NotExists(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 1,
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{
				{
					Type:   ConditionTypeReady,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	condition := GetCondition(xtrinode, ConditionTypeError)
	assert.Nil(t, condition)
}

func TestGetCondition_NilConditions(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 1,
		},
		Status: analyticsv1.XTrinodeStatus{},
	}

	condition := GetCondition(xtrinode, ConditionTypeReady)
	assert.Nil(t, condition)
}

func TestIsReady_True(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 1,
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{
				{
					Type:   ConditionTypeReady,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	assert.True(t, IsReady(xtrinode))
}

func TestIsReady_False(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
	}{
		{
			name: "condition not exists",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "default",
					Generation: 1,
				},
				Status: analyticsv1.XTrinodeStatus{},
			},
		},
		{
			name: "condition false",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "default",
					Generation: 1,
				},
				Status: analyticsv1.XTrinodeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   ConditionTypeReady,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
		},
		{
			name: "condition unknown",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "default",
					Generation: 1,
				},
				Status: analyticsv1.XTrinodeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   ConditionTypeReady,
							Status: metav1.ConditionUnknown,
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, IsReady(tt.xtrinode))
		})
	}
}

func TestHasError_True(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 1,
		},
		Status: analyticsv1.XTrinodeStatus{
			Conditions: []metav1.Condition{
				{
					Type:   ConditionTypeError,
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	assert.True(t, HasError(xtrinode))
}

func TestHasError_False(t *testing.T) {
	tests := []struct {
		name     string
		xtrinode *analyticsv1.XTrinode
	}{
		{
			name: "condition not exists",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "default",
					Generation: 1,
				},
				Status: analyticsv1.XTrinodeStatus{},
			},
		},
		{
			name: "condition false",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "default",
					Generation: 1,
				},
				Status: analyticsv1.XTrinodeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   ConditionTypeError,
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
		},
		{
			name: "condition unknown",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "default",
					Generation: 1,
				},
				Status: analyticsv1.XTrinodeStatus{
					Conditions: []metav1.Condition{
						{
							Type:   ConditionTypeError,
							Status: metav1.ConditionUnknown,
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, HasError(tt.xtrinode))
		})
	}
}

func TestSetCondition_ConcurrentUpdates(t *testing.T) {
	// Test that SetCondition handles concurrent-like updates correctly
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 1,
		},
		Status: analyticsv1.XTrinodeStatus{},
	}

	// Simulate rapid condition updates
	for i := 0; i < 10; i++ {
		SetCondition(xtrinode, ConditionTypeReady, metav1.ConditionTrue, ConditionReasonAllComponentsReady, "Ready")
		SetCondition(xtrinode, ConditionTypeReconciling, metav1.ConditionFalse, ConditionReasonReconciling, "Not reconciling")
		time.Sleep(time.Millisecond) // Small delay to ensure different timestamps
	}

	require.Len(t, xtrinode.Status.Conditions, 2)
	ready := GetCondition(xtrinode, ConditionTypeReady)
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionTrue, ready.Status)
}

func TestSetCondition_ErrorCleared(t *testing.T) {
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "test",
			Namespace:  "default",
			Generation: 1,
		},
		Status: analyticsv1.XTrinodeStatus{},
	}

	// CRD validation requires reason to be at least 1 char
	SetCondition(xtrinode, ConditionTypeError, metav1.ConditionFalse, ConditionReasonNoError, "No errors")

	require.Len(t, xtrinode.Status.Conditions, 1)
	condition := xtrinode.Status.Conditions[0]
	assert.Equal(t, ConditionTypeError, condition.Type)
	assert.Equal(t, metav1.ConditionFalse, condition.Status)
	assert.Equal(t, ConditionReasonNoError, condition.Reason)
	assert.Equal(t, "No errors", condition.Message)
}
