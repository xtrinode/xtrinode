package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// ComputeXTrinodeRevision computes a deterministic base revision (identity/debug)
// This is a STABLE identifier used for resource naming and debugging only
// It does NOT include catalogs - catalog changes are tracked via rollout hashes
//
// Components included in hash:
//   - xtrinode.Spec (main source of truth)
//   - operatorVersion (affects resource building)
//
// Catalogs are explicitly EXCLUDED from the base revision because catalog
// sensitivity is handled via per-component rollout hashes.
//
// Returns: Short hash (first 12 characters) for readability
func ComputeXTrinodeRevision(xtrinode *analyticsv1.XTrinode, operatorVersion string, catalogs []string) string {
	hasher := sha256.New()

	// Hash spec (main source of truth)
	// Use a stable JSON encoding (sorted keys)
	specBytes, err := json.Marshal(xtrinode.Spec)
	if err != nil {
		// Fallback: use string representation
		specBytes = []byte(fmt.Sprintf("%+v", xtrinode.Spec))
	}
	hasher.Write(specBytes)

	// Hash operator version (affects resource building)
	hasher.Write([]byte(operatorVersion))

	// Catalogs are intentionally EXCLUDED from base revision.
	// They are tracked separately via rollout hashes.

	// Return short hash (first 8 chars) for readability
	hash := hex.EncodeToString(hasher.Sum(nil))
	return hash[:8]
}

// GetXTrinodeRevision computes the base revision (always recomputes)
// No caching since operatorVersion changes must be detected but aren't tracked in status
// Exported for use in controller to update status
func GetXTrinodeRevision(xtrinode *analyticsv1.XTrinode, operatorVersion string, catalogs []string) string {
	// Always recompute to ensure operator version changes are detected
	// Caching by ObservedGeneration alone would miss operator upgrades
	return ComputeXTrinodeRevision(xtrinode, operatorVersion, catalogs)
}

// StampRevision adds revision labels and annotations to an object
func StampRevision(obj metav1.Object, revision string) {
	// Add label
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[config.RevisionLabelKey] = revision
	obj.SetLabels(labels)

	// Add annotation
	annotations := obj.GetAnnotations()
	if annotations == nil {
		annotations = make(map[string]string)
	}
	annotations[config.RevisionAnnotationKey] = revision
	obj.SetAnnotations(annotations)
}

// StampRevisionOnPodTemplate adds revision annotation to PodTemplate
// This forces a rollout when revision changes
func StampRevisionOnPodTemplate(template *corev1.PodTemplateSpec, revision string) {
	if template.Annotations == nil {
		template.Annotations = make(map[string]string)
	}
	template.Annotations[config.RevisionAnnotationKey] = revision
}
