package controllers

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestProcessCommands_ResumeLeavesWakeAnnotationsForResumeStep(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	key := types.NamespacedName{Name: "runtime", Namespace: "team-a"}
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
			Annotations: map[string]string{
				config.ResumeRequestedAnnotation:   "true",
				config.ResumeRequestedAtAnnotation: time.Now().Format(time.RFC3339),
				config.WakeMinWorkersAnnotation:    "3",
				config.WakeTTLAnnotation:           "10m",
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:      "s",
			Suspended: true,
		},
	}
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		Build()
	reconciler := newTestReconciler(cli, scheme)

	commands, err := reconciler.ProcessCommands(ctx, xtrinode)
	require.NoError(t, err)
	require.Len(t, commands, 1)
	assert.Equal(t, CommandResume, commands[0].Type)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(ctx, key, &updated))
	assert.False(t, updated.Spec.Suspended)
	assert.Empty(t, updated.Annotations[config.ResumeRequestedAnnotation])
	assert.Empty(t, updated.Annotations[config.ResumeRequestedAtAnnotation])
	assert.Equal(t, "3", updated.Annotations[config.WakeMinWorkersAnnotation])
	assert.Equal(t, "10m", updated.Annotations[config.WakeTTLAnnotation])
}

func TestProcessCommands_SuspendWinningOverResumeClearsWakeAnnotations(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	key := types.NamespacedName{Name: "runtime", Namespace: "team-a"}
	now := time.Now()
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
			Annotations: map[string]string{
				config.ResumeRequestedAnnotation:    "true",
				config.ResumeRequestedAtAnnotation:  now.Add(-1 * time.Minute).Format(time.RFC3339),
				config.SuspendRequestedAnnotation:   "true",
				config.SuspendRequestedAtAnnotation: now.Format(time.RFC3339),
				config.WakeMinWorkersAnnotation:     "3",
				config.WakeTTLAnnotation:            "10m",
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:      "s",
			Suspended: false,
		},
	}
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		Build()
	reconciler := newTestReconciler(cli, scheme)

	commands, err := reconciler.ProcessCommands(ctx, xtrinode)
	require.NoError(t, err)
	require.Len(t, commands, 1)
	assert.Equal(t, CommandSuspend, commands[0].Type)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(ctx, key, &updated))
	assert.True(t, updated.Spec.Suspended)
	assert.Empty(t, updated.Annotations[config.ResumeRequestedAnnotation])
	assert.Empty(t, updated.Annotations[config.ResumeRequestedAtAnnotation])
	assert.Empty(t, updated.Annotations[config.SuspendRequestedAnnotation])
	assert.Empty(t, updated.Annotations[config.SuspendRequestedAtAnnotation])
	assert.Empty(t, updated.Annotations[config.WakeMinWorkersAnnotation])
	assert.Empty(t, updated.Annotations[config.WakeTTLAnnotation])
}

func TestProcessCommands_AutoSuspendClearsOrphanWakeAnnotations(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	key := types.NamespacedName{Name: "runtime", Namespace: "team-a"}
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
			Annotations: map[string]string{
				config.AutoSuspendRequestedAnnotation:   "true",
				config.AutoSuspendRequestedAtAnnotation: time.Now().Format(time.RFC3339),
				config.WakeMinWorkersAnnotation:         "5",
				config.WakeTTLAnnotation:                "15m",
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:      "s",
			Suspended: false,
		},
	}
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(xtrinode).
		Build()
	reconciler := newTestReconciler(cli, scheme)

	commands, err := reconciler.ProcessCommands(ctx, xtrinode)
	require.NoError(t, err)
	require.Len(t, commands, 1)
	assert.Equal(t, CommandAutoSuspend, commands[0].Type)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(ctx, key, &updated))
	assert.True(t, updated.Spec.Suspended)
	assert.Empty(t, updated.Annotations[config.AutoSuspendRequestedAnnotation])
	assert.Empty(t, updated.Annotations[config.AutoSuspendRequestedAtAnnotation])
	assert.Empty(t, updated.Annotations[config.WakeMinWorkersAnnotation])
	assert.Empty(t, updated.Annotations[config.WakeTTLAnnotation])
}

func TestProcessCommands_InvalidResumePreservesWakeAnnotationsForRetry(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme()
	key := types.NamespacedName{Name: "runtime", Namespace: "team-a"}
	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      key.Name,
			Namespace: key.Namespace,
			Annotations: map[string]string{
				config.ResumeRequestedAnnotation:   "true",
				config.ResumeRequestedAtAnnotation: time.Now().Format(time.RFC3339),
				config.WakeMinWorkersAnnotation:    "-1",
				config.WakeTTLAnnotation:           "10m",
			},
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:      "s",
			Suspended: true,
		},
	}
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&analyticsv1.XTrinode{}).
		WithObjects(xtrinode).
		Build()
	reconciler := newTestReconciler(cli, scheme)

	commands, err := reconciler.ProcessCommands(ctx, xtrinode)
	require.NoError(t, err)
	assert.Empty(t, commands)

	var updated analyticsv1.XTrinode
	require.NoError(t, cli.Get(ctx, key, &updated))
	assert.True(t, updated.Spec.Suspended)
	assert.Equal(t, "true", updated.Annotations[config.ResumeRequestedAnnotation])
	assert.Equal(t, "-1", updated.Annotations[config.WakeMinWorkersAnnotation])
	assert.Equal(t, "10m", updated.Annotations[config.WakeTTLAnnotation])

	ready := status.GetCondition(&updated, status.ConditionTypeReady)
	require.NotNil(t, ready)
	assert.Equal(t, metav1.ConditionFalse, ready.Status)
	assert.Equal(t, "CommandRejected", ready.Reason)
}
