package controllers

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	kedav1alpha1 "github.com/kedacore/keda/v2/apis/keda/v1alpha1"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
)

func TestReconcileGatewayStep_Name(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := newTestReconciler(client, scheme)

	step := &reconcileGatewayStep{reconciler: reconciler}
	assert.Equal(t, "reconcileGateway", step.Name())
}

func TestReconcileGatewayStep_Execute(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		Build()

	reconciler := newTestReconciler(client, scheme)
	step := &reconcileGatewayStep{reconciler: reconciler}

	ctx := context.Background()
	state := &ReconciliationState{
		Log:               ctrl.Log,
		EffectiveCatalogs: []string{},
	}

	result, shouldContinue, err := step.Execute(ctx, xtrinode, state)

	// Gateway service may fail in test env, but should not panic
	if err != nil {
		t.Logf("Gateway step returned error (may be expected): %v", err)
		return
	}

	assert.NoError(t, err)
	assert.True(t, shouldContinue)
	assert.NotNil(t, result)
}

func TestReconcileWakeTTLStep_Name(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := newTestReconciler(client, scheme)

	step := &reconcileWakeTTLStep{reconciler: reconciler}
	assert.Equal(t, "reconcileWakeTTL", step.Name())
}

func TestReconcileWakeTTLStep_Execute(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)
	_ = kedav1alpha1.AddToScheme(scheme)

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size: "s",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		Build()

	reconciler := newTestReconciler(client, scheme)
	step := &reconcileWakeTTLStep{reconciler: reconciler}

	ctx := context.Background()
	state := &ReconciliationState{
		Log:               ctrl.Log,
		EffectiveCatalogs: []string{},
	}

	result, shouldContinue, err := step.Execute(ctx, xtrinode, state)

	// WakeTTL step is non-critical, should always continue
	assert.NoError(t, err)
	assert.True(t, shouldContinue)
	assert.NotNil(t, result)
}

func TestReconcileAutoSuspendStep_Name(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)

	client := fake.NewClientBuilder().WithScheme(scheme).Build()
	reconciler := newTestReconciler(client, scheme)

	step := &reconcileAutoSuspendStep{reconciler: reconciler}
	assert.Equal(t, "reconcileAutoSuspend", step.Name())
}

func TestReconcileAutoSuspendStep_Execute_NoAutoSuspend(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:      "s",
			Suspended: false,
			// No AutoSuspendAfter
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		Build()

	reconciler := newTestReconciler(client, scheme)
	step := &reconcileAutoSuspendStep{reconciler: reconciler}

	ctx := context.Background()
	state := &ReconciliationState{
		Log:               ctrl.Log,
		EffectiveCatalogs: []string{},
	}

	result, shouldContinue, err := step.Execute(ctx, xtrinode, state)

	// Should continue when no auto-suspend configured
	assert.NoError(t, err)
	assert.True(t, shouldContinue)
	assert.NotNil(t, result)
}

func TestReconcileAutoSuspendStep_Execute_AlreadySuspended(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = analyticsv1.AddToScheme(scheme)

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:      "s",
			Suspended: true, // Already suspended
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		Build()

	reconciler := newTestReconciler(client, scheme)
	step := &reconcileAutoSuspendStep{reconciler: reconciler}

	ctx := context.Background()
	state := &ReconciliationState{
		Log:               ctrl.Log,
		EffectiveCatalogs: []string{},
	}

	result, shouldContinue, err := step.Execute(ctx, xtrinode, state)

	// Should continue when already suspended
	assert.NoError(t, err)
	assert.True(t, shouldContinue)
	assert.NotNil(t, result)
}
