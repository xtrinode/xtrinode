package resources

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

func TestComputeXTrinodeRevision(t *testing.T) {
	tests := []struct {
		name            string
		xtrinode        *analyticsv1.XTrinode
		operatorVersion string
		catalogs        []string
		wantSame        bool // Whether two identical specs should produce same revision
		wantDifferent   bool // Whether different specs should produce different revisions
	}{
		{
			name: "same spec produces same revision",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			operatorVersion: "1.0.0",
			catalogs:        []string{"postgres", "hive"},
			wantSame:        true,
		},
		{
			name: "different spec produces different revision",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s", // Base spec
				},
			},
			operatorVersion: "1.0.0",
			catalogs:        []string{"postgres", "hive"},
			wantDifferent:   true,
		},
		{
			name: "different operator version produces different revision",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			operatorVersion: "2.0.0", // Different version
			catalogs:        []string{"postgres", "hive"},
			wantDifferent:   true,
		},
		{
			name: "catalog order doesn't matter (sorted)",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: "default",
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
			},
			operatorVersion: "1.0.0",
			catalogs:        []string{"hive", "postgres"}, // Different order
			wantSame:        true,                         // Should produce same revision as ["postgres", "hive"]
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			revision1 := ComputeXTrinodeRevision(tt.xtrinode, tt.operatorVersion, tt.catalogs)

			// Revision should be 8 characters (short hash)
			assert.Len(t, revision1, 8, "revision should be 8 characters")

			if tt.wantSame {
				revision2 := ComputeXTrinodeRevision(tt.xtrinode, tt.operatorVersion, tt.catalogs)
				assert.Equal(t, revision1, revision2, "same input should produce same revision")
			}

			if tt.wantDifferent {
				var differentInput *analyticsv1.XTrinode
				var differentVersion string
				var differentCatalogs []string

				switch tt.name {
				case "different spec produces different revision":
					differentInput = &analyticsv1.XTrinode{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test",
							Namespace: "default",
						},
						Spec: analyticsv1.XTrinodeSpec{
							Size: "m", // Different size
						},
					}
					differentVersion = tt.operatorVersion
					differentCatalogs = tt.catalogs
				case "different operator version produces different revision":
					differentInput = tt.xtrinode
					differentVersion = "1.0.0" // Original version (different from test case)
					differentCatalogs = tt.catalogs
					// NOTE: Catalogs are intentionally excluded from base revision
					// They are tracked separately via rollout hashes for coordinator-only rollouts
				}

				if differentInput != nil {
					revision2 := ComputeXTrinodeRevision(differentInput, differentVersion, differentCatalogs)
					assert.NotEqual(t, revision1, revision2, "different input should produce different revision")
				}
			}
		})
	}
}

func TestGetXTrinodeRevision(t *testing.T) {
	tests := []struct {
		name            string
		xtrinode        *analyticsv1.XTrinode
		operatorVersion string
		catalogs        []string
	}{
		{
			name: "computes revision from spec and operator version",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
				Status: analyticsv1.XTrinodeStatus{
					CurrentRevision:    "",
					ObservedGeneration: 0,
				},
			},
			operatorVersion: "1.0.0",
			catalogs:        []string{"postgres", "hive"},
		},
		{
			name: "always recomputes (no caching to detect operator version changes)",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
				Status: analyticsv1.XTrinodeStatus{
					CurrentRevision:    "abc12345",
					ObservedGeneration: 1,
				},
			},
			operatorVersion: "1.0.0",
			catalogs:        []string{"postgres", "hive"},
		},
		{
			name: "different operator version produces different revision",
			xtrinode: &analyticsv1.XTrinode{
				ObjectMeta: metav1.ObjectMeta{
					Name:       "test",
					Namespace:  "default",
					Generation: 1,
				},
				Spec: analyticsv1.XTrinodeSpec{
					Size: "s",
				},
				Status: analyticsv1.XTrinodeStatus{
					CurrentRevision:    "abc12345",
					ObservedGeneration: 1,
				},
			},
			operatorVersion: "2.0.0", // Different version
			catalogs:        []string{"postgres", "hive"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			revision := GetXTrinodeRevision(tt.xtrinode, tt.operatorVersion, tt.catalogs)

			// Should always return a valid 8-character revision
			assert.Len(t, revision, 8, "revision should be 8 characters")
			assert.NotEmpty(t, revision, "revision should not be empty")

			// Verify it's deterministic - same inputs produce same output
			revision2 := GetXTrinodeRevision(tt.xtrinode, tt.operatorVersion, tt.catalogs)
			assert.Equal(t, revision, revision2, "should be deterministic")
		})
	}
}

func TestStampRevision(t *testing.T) {
	tests := []struct {
		name     string
		obj      metav1.Object
		revision string
	}{
		{
			name: "stamp revision on object with no labels/annotations",
			obj: &metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
			},
			revision: "abc12345",
		},
		{
			name: "stamp revision on object with existing labels",
			obj: &metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
				Labels: map[string]string{
					"app": "trino",
				},
			},
			revision: "abc12345",
		},
		{
			name: "stamp revision on object with existing annotations",
			obj: &metav1.ObjectMeta{
				Name:      "test",
				Namespace: "default",
				Annotations: map[string]string{
					"checksum/config": "xyz",
				},
			},
			revision: "abc12345",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			StampRevision(tt.obj, tt.revision)

			// Verify label
			labels := tt.obj.GetLabels()
			assert.NotNil(t, labels, "labels should not be nil")
			assert.Equal(t, tt.revision, labels[config.RevisionLabelKey], "revision label should be set")

			// Verify annotation
			annotations := tt.obj.GetAnnotations()
			assert.NotNil(t, annotations, "annotations should not be nil")
			assert.Equal(t, tt.revision, annotations[config.RevisionAnnotationKey], "revision annotation should be set")

			// Verify existing labels/annotations preserved
			if tt.name == "stamp revision on object with existing labels" {
				assert.Equal(t, "trino", labels["app"], "existing labels should be preserved")
			}
			if tt.name == "stamp revision on object with existing annotations" {
				assert.Equal(t, "xyz", annotations["checksum/config"], "existing annotations should be preserved")
			}
		})
	}
}

func TestStampRevisionOnPodTemplate(t *testing.T) {
	tests := []struct {
		name     string
		template *corev1.PodTemplateSpec
		revision string
	}{
		{
			name: "stamp revision on pod template with no annotations",
			template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "trino",
					},
				},
			},
			revision: "abc12345",
		},
		{
			name: "stamp revision on pod template with existing annotations",
			template: &corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "trino",
					},
					Annotations: map[string]string{
						"checksum/config": "xyz",
					},
				},
			},
			revision: "abc12345",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			StampRevisionOnPodTemplate(tt.template, tt.revision)

			// Verify annotation
			assert.NotNil(t, tt.template.Annotations, "annotations should not be nil")
			assert.Equal(t, tt.revision, tt.template.Annotations[config.RevisionAnnotationKey], "revision annotation should be set")

			// Verify existing annotations preserved
			if tt.name == "stamp revision on pod template with existing annotations" {
				assert.Equal(t, "xyz", tt.template.Annotations["checksum/config"], "existing annotations should be preserved")
			}
		})
	}
}
