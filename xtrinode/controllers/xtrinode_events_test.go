package controllers

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/events"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestProcessCommandsEmitsCommandEvents(t *testing.T) {
	tests := []struct {
		name       string
		xtrinode   *analyticsv1.XTrinode
		wantType   CommandType
		wantReason string
		wantLevel  string
	}{
		{
			name: "resume command emits normal event",
			xtrinode: eventTestXTrinode(map[string]string{
				config.ResumeRequestedAnnotation:   "true",
				config.ResumeRequestedAtAnnotation: time.Now().Format(time.RFC3339),
				config.WakeMinWorkersAnnotation:    "2",
				config.WakeTTLAnnotation:           "10m",
			}, true),
			wantType:   CommandResume,
			wantReason: events.ReasonResumeRequested,
			wantLevel:  "Normal",
		},
		{
			name: "suspend command emits normal event",
			xtrinode: eventTestXTrinode(map[string]string{
				config.SuspendRequestedAnnotation:   "true",
				config.SuspendRequestedAtAnnotation: time.Now().Format(time.RFC3339),
			}, false),
			wantType:   CommandSuspend,
			wantReason: events.ReasonSuspendRequested,
			wantLevel:  "Normal",
		},
		{
			name: "invalid resume command emits warning event",
			xtrinode: eventTestXTrinode(map[string]string{
				config.ResumeRequestedAnnotation:   "true",
				config.ResumeRequestedAtAnnotation: time.Now().Format(time.RFC3339),
				config.WakeMinWorkersAnnotation:    "-1",
				config.WakeTTLAnnotation:           "10m",
			}, true),
			wantReason: events.ReasonResumeRequested,
			wantLevel:  "Warning",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			reconciler, fakeRecorder := eventTestReconciler(t, tt.xtrinode)

			commands, err := reconciler.ProcessCommands(ctx, tt.xtrinode)
			require.NoError(t, err)
			if tt.wantType != "" {
				require.Len(t, commands, 1)
				assert.Equal(t, tt.wantType, commands[0].Type)
			}

			event := readRecordedEvent(t, fakeRecorder)
			assert.Contains(t, event, tt.wantLevel)
			assert.Contains(t, event, tt.wantReason)
		})
	}
}

func TestReconciliationPipelineEmitsStepFailureEvent(t *testing.T) {
	xtrinode := eventTestXTrinode(nil, false)
	reconciler, fakeRecorder := eventTestReconciler(t, xtrinode)
	pipeline := &ReconciliationPipeline{
		steps: []ReconciliationStep{
			&eventFailureStep{reconciler: reconciler},
		},
	}

	result, err := pipeline.Execute(context.Background(), xtrinode, ctrl.Log)
	require.Error(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	event := readRecordedEvent(t, fakeRecorder)
	assert.Contains(t, event, "Warning")
	assert.Contains(t, event, events.ReasonReconcileError)
	assert.Contains(t, event, "Step eventFailure failed")
}

type eventFailureStep struct {
	reconciler *XTrinodeReconciler
}

func (s *eventFailureStep) Name() string {
	return "eventFailure"
}

func (s *eventFailureStep) Execute(context.Context, *analyticsv1.XTrinode, *ReconciliationState) (result ctrl.Result, shouldContinue bool, err error) {
	return ctrl.Result{}, false, errors.New("planned event failure")
}

func (s *eventFailureStep) getReconciler() *XTrinodeReconciler {
	return s.reconciler
}

func eventTestXTrinode(annotations map[string]string, suspended bool) *analyticsv1.XTrinode {
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "runtime",
			Namespace:   "team-a",
			Annotations: annotations,
		},
		Spec: analyticsv1.XTrinodeSpec{
			Size:      "s",
			Suspended: suspended,
		},
	}
}

func eventTestReconciler(t *testing.T, objects ...client.Object) (*XTrinodeReconciler, *record.FakeRecorder) {
	t.Helper()
	scheme := newTestScheme()
	cli := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		Build()
	fakeRecorder := record.NewFakeRecorder(20)
	recorder := events.NewRecorder(fakeRecorder, events.DefaultConfig())
	return &XTrinodeReconciler{
		Client:        cli,
		Scheme:        scheme,
		EventRecorder: recorder,
	}, fakeRecorder
}

func readRecordedEvent(t *testing.T, fakeRecorder *record.FakeRecorder) string {
	t.Helper()
	select {
	case event := <-fakeRecorder.Events:
		return event
	case <-time.After(2 * time.Second):
		t.Fatal("expected Kubernetes event but none was recorded")
		return ""
	}
}
