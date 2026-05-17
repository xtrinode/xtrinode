package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

// testObject is a simple runtime.Object for testing
type testObject struct {
	metav1.TypeMeta
	metav1.ObjectMeta
}

func (t *testObject) DeepCopyObject() runtime.Object {
	copied := testObject{
		TypeMeta:   t.TypeMeta,
		ObjectMeta: *t.DeepCopy(),
	}
	return &copied
}

func (t *testObject) DeepCopy() *metav1.ObjectMeta {
	return t.ObjectMeta.DeepCopy()
}

func TestRecorder_Event(t *testing.T) {
	tests := []struct {
		name      string
		config    Config
		eventType string
		reason    string
		message   string
		wantEvent bool
	}{
		{
			name:      "enabled recorder records event",
			config:    DefaultConfig().WithEnabled(true),
			eventType: corev1.EventTypeNormal,
			reason:    ReasonCreated,
			message:   "Test message",
			wantEvent: true,
		},
		{
			name:      "disabled recorder ignores event",
			config:    DefaultConfig().WithEnabled(false),
			eventType: corev1.EventTypeNormal,
			reason:    ReasonCreated,
			message:   "Test message",
			wantEvent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeRecorder := record.NewFakeRecorder(10)
			rec := NewRecorder(fakeRecorder, tt.config)

			obj := &testObject{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
			}

			rec.Event(obj, tt.eventType, tt.reason, tt.message)

			if tt.wantEvent {
				select {
				case event := <-fakeRecorder.Events:
					assert.Contains(t, event, tt.eventType)
					assert.Contains(t, event, tt.reason)
					assert.Contains(t, event, tt.message)
				default:
					t.Fatal("expected event but none was recorded")
				}
			} else {
				select {
				case <-fakeRecorder.Events:
					t.Fatal("unexpected event recorded")
				default:
					// Expected: no event
				}
			}
		})
	}
}

func TestRecorder_Normal(t *testing.T) {
	fakeRecorder := record.NewFakeRecorder(10)
	rec := NewRecorder(fakeRecorder, DefaultConfig())

	obj := &testObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	rec.Normal(obj, ReasonCreated, "Test normal event")

	event := <-fakeRecorder.Events
	assert.Contains(t, event, corev1.EventTypeNormal)
	assert.Contains(t, event, ReasonCreated)
	assert.Contains(t, event, "Test normal event")
}

func TestRecorder_Warning(t *testing.T) {
	fakeRecorder := record.NewFakeRecorder(10)
	rec := NewRecorder(fakeRecorder, DefaultConfig())

	obj := &testObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	rec.Warning(obj, ReasonReconcileError, "Test warning event")

	event := <-fakeRecorder.Events
	assert.Contains(t, event, corev1.EventTypeWarning)
	assert.Contains(t, event, ReasonReconcileError)
	assert.Contains(t, event, "Test warning event")
}

func TestRecorder_Eventf(t *testing.T) {
	fakeRecorder := record.NewFakeRecorder(10)
	rec := NewRecorder(fakeRecorder, DefaultConfig())

	obj := &testObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	rec.Eventf(obj, corev1.EventTypeNormal, ReasonWorkersScaledUp,
		"Workers scaled from %d to %d", 2, 5)

	event := <-fakeRecorder.Events
	assert.Contains(t, event, corev1.EventTypeNormal)
	assert.Contains(t, event, ReasonWorkersScaledUp)
	assert.Contains(t, event, "Workers scaled from 2 to 5")
}

func TestRecorder_Normalf(t *testing.T) {
	fakeRecorder := record.NewFakeRecorder(10)
	rec := NewRecorder(fakeRecorder, DefaultConfig())

	obj := &testObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	rec.Normalf(obj, ReasonCreated, "XTrinode %s/%s created", "default", "test")

	event := <-fakeRecorder.Events
	assert.Contains(t, event, corev1.EventTypeNormal)
	assert.Contains(t, event, ReasonCreated)
	assert.Contains(t, event, "XTrinode default/test created")
}

func TestRecorder_Warningf(t *testing.T) {
	fakeRecorder := record.NewFakeRecorder(10)
	rec := NewRecorder(fakeRecorder, DefaultConfig())

	obj := &testObject{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "default",
		},
	}

	rec.Warningf(obj, ReasonReconcileError, "Failed to reconcile %s/%s: %v", "default", "test", assert.AnError)

	event := <-fakeRecorder.Events
	assert.Contains(t, event, corev1.EventTypeWarning)
	assert.Contains(t, event, ReasonReconcileError)
	assert.Contains(t, event, "Failed to reconcile default/test")
}

func TestConfig_WithComponentName(t *testing.T) {
	config := DefaultConfig()
	assert.Equal(t, "xtrinode-operator", config.ComponentName)

	newConfig := config.WithComponentName("custom-controller")
	assert.Equal(t, "custom-controller", newConfig.ComponentName)
	assert.Equal(t, "xtrinode-operator", config.ComponentName) // Original unchanged
}

func TestConfig_WithEnabled(t *testing.T) {
	config := DefaultConfig()
	assert.True(t, config.Enabled)

	newConfig := config.WithEnabled(false)
	assert.False(t, newConfig.Enabled)
	assert.True(t, config.Enabled) // Original unchanged
}
