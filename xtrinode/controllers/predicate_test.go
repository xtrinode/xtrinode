package controllers

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

func TestXTrinodePredicateLifecycleEvents(t *testing.T) {
	predicate := xtrinodePredicate()

	xtrinode := &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "runtime",
			Namespace: "default",
		},
	}

	assert.True(t, predicate.Create(event.CreateEvent{Object: xtrinode}))
	assert.True(t, predicate.Delete(event.DeleteEvent{Object: xtrinode}))
}

func TestXTrinodePredicateUpdateTriggers(t *testing.T) {
	predicate := xtrinodePredicate()

	t.Run("generation change", func(t *testing.T) {
		oldXTrinode := predicateTestXTrinode(1, nil, nil)
		newXTrinode := predicateTestXTrinode(2, nil, nil)

		assert.True(t, predicate.Update(event.UpdateEvent{
			ObjectOld: oldXTrinode,
			ObjectNew: newXTrinode,
		}))
	})

	t.Run("deletion timestamp transition", func(t *testing.T) {
		now := metav1.Now()
		oldXTrinode := predicateTestXTrinode(1, nil, nil)
		newXTrinode := predicateTestXTrinode(1, nil, &now)

		assert.True(t, predicate.Update(event.UpdateEvent{
			ObjectOld: oldXTrinode,
			ObjectNew: newXTrinode,
		}))
	})

	t.Run("non XTrinode objects are not filtered", func(t *testing.T) {
		assert.True(t, predicate.Update(event.UpdateEvent{
			ObjectOld: &corev1.ConfigMap{},
			ObjectNew: &corev1.ConfigMap{},
		}))
	})
}

func TestXTrinodePredicateCommandAnnotations(t *testing.T) {
	predicate := xtrinodePredicate()
	commandAnnotations := []string{
		config.ResumeRequestedAnnotation,
		config.ResumeRequestedAtAnnotation,
		config.SuspendRequestedAnnotation,
		config.SuspendRequestedAtAnnotation,
		config.AutoSuspendRequestedAnnotation,
		config.AutoSuspendRequestedAtAnnotation,
		config.WakeMinWorkersAnnotation,
		config.WakeTTLAnnotation,
	}

	for _, key := range commandAnnotations {
		t.Run(key, func(t *testing.T) {
			oldXTrinode := predicateTestXTrinode(1, nil, nil)
			newXTrinode := predicateTestXTrinode(1, map[string]string{key: "changed"}, nil)

			assert.True(t, predicate.Update(event.UpdateEvent{
				ObjectOld: oldXTrinode,
				ObjectNew: newXTrinode,
			}))
		})
	}
}

func TestXTrinodePredicateIgnoresStatusAndIrrelevantMetadata(t *testing.T) {
	predicate := xtrinodePredicate()

	oldXTrinode := predicateTestXTrinode(1, map[string]string{"unrelated": "old"}, nil)
	oldXTrinode.Status.Phase = "Reconciling"
	oldXTrinode.SetResourceVersion("1")

	newXTrinode := predicateTestXTrinode(1, map[string]string{"unrelated": "new"}, nil)
	newXTrinode.Status.Phase = "Ready"
	newXTrinode.SetResourceVersion("2")

	assert.False(t, predicate.Update(event.UpdateEvent{
		ObjectOld: oldXTrinode,
		ObjectNew: newXTrinode,
	}))
}

func predicateTestXTrinode(generation int64, annotations map[string]string, deletionTimestamp *metav1.Time) *analyticsv1.XTrinode {
	return &analyticsv1.XTrinode{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "runtime",
			Namespace:         "default",
			Generation:        generation,
			Annotations:       annotations,
			DeletionTimestamp: deletionTimestamp,
		},
	}
}
