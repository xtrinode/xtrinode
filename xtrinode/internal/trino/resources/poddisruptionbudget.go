package resources

import (
	"fmt"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// parseIntOrString converts interface{} to intstr.IntOrString
// Handles int64, float64, and string types from YAML/JSON unmarshaling
func parseIntOrString(v interface{}) (intstr.IntOrString, bool) {
	switch val := v.(type) {
	case int64:
		return intstr.FromInt32(int32(val)), true
	case float64:
		return intstr.FromInt32(int32(val)), true
	case string:
		return intstr.FromString(val), true
	default:
		return intstr.FromInt32(0), false
	}
}

// BuildCoordinatorPodDisruptionBudget builds a PodDisruptionBudget for the coordinator
func BuildCoordinatorPodDisruptionBudget(xtrinode *analyticsv1.XTrinode) *policyv1.PodDisruptionBudget {
	// Default: enabled=true, minAvailable=1 (coordinator should always be available)
	enabled := true
	minAvailable := intstr.FromInt32(config.DefaultCoordinatorPDBMinAvailable)
	maxUnavailable := intstr.FromInt32(0)

	// Check valuesOverlay for PDB configuration
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if coordinator, ok := xtrinode.Spec.GetValuesOverlayMap()["coordinator"].(map[string]interface{}); ok {
			if pdb, ok := coordinator["podDisruptionBudget"].(map[string]interface{}); ok {
				// Check if enabled (default: true for coordinator)
				if enabledVal, ok := pdb["enabled"].(bool); ok {
					enabled = enabledVal
				}

				if !enabled {
					return nil // PDB disabled
				}

				// Parse minAvailable
				if val, ok := parseIntOrString(pdb["minAvailable"]); ok {
					minAvailable = val
					maxUnavailable = intstr.FromInt32(0)
				}

				// If user explicitly sets maxUnavailable, prefer it over minAvailable
				// Parse maxUnavailable (only if minAvailable not explicitly set in valuesOverlay)
				if _, minAvailSet := pdb["minAvailable"]; !minAvailSet {
					if val, ok := parseIntOrString(pdb["maxUnavailable"]); ok {
						maxUnavailable = val
						minAvailable = intstr.FromInt32(0)
					}
				}
			}
		}
	}

	if !enabled {
		return nil
	}

	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:            coordinatorPDBName(xtrinode),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: func() *intstr.IntOrString {
				if minAvailable.IntVal == 0 && minAvailable.StrVal == "" {
					return nil
				}
				return &minAvailable
			}(),
			MaxUnavailable: func() *intstr.IntOrString {
				if maxUnavailable.IntVal == 0 && maxUnavailable.StrVal == "" {
					return nil
				}
				return &maxUnavailable
			}(),
			Selector: &metav1.LabelSelector{
				MatchLabels: TrinoSelectorLabels(xtrinode, ComponentCoordinator),
			},
		},
	}
}

// BuildWorkerPodDisruptionBudget builds a PodDisruptionBudget for the workers
func BuildWorkerPodDisruptionBudget(xtrinode *analyticsv1.XTrinode) *policyv1.PodDisruptionBudget {
	// Default: enabled=true, maxUnavailable=1 (allow 1 worker to be disrupted at a time)
	enabled := true
	maxUnavailable := intstr.FromInt32(config.DefaultWorkerPDBMaxUnavailable)
	minAvailable := intstr.FromInt32(0)

	// Check valuesOverlay for PDB configuration
	if xtrinode.Spec.GetValuesOverlayMap() != nil {
		if worker, ok := xtrinode.Spec.GetValuesOverlayMap()["worker"].(map[string]interface{}); ok {
			if pdb, ok := worker["podDisruptionBudget"].(map[string]interface{}); ok {
				// Check if enabled (default: true for workers)
				if enabledVal, ok := pdb["enabled"].(bool); ok {
					enabled = enabledVal
				}

				if !enabled {
					return nil // PDB disabled
				}

				// Parse minAvailable
				if val, ok := parseIntOrString(pdb["minAvailable"]); ok {
					minAvailable = val
					maxUnavailable = intstr.FromInt32(0)
				}

				// If user explicitly sets maxUnavailable, prefer it over minAvailable
				// Parse maxUnavailable (only if minAvailable not explicitly set)
				if minAvailable.Type == intstr.Int && minAvailable.IntVal == 0 {
					if val, ok := parseIntOrString(pdb["maxUnavailable"]); ok {
						maxUnavailable = val
						minAvailable = intstr.FromInt32(0)
					}
				}
			}
		}
	}

	if !enabled {
		return nil
	}

	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:            workerPDBName(xtrinode),
			Namespace:       xtrinode.Namespace,
			Labels:          TrinoLabels(xtrinode),
			OwnerReferences: []metav1.OwnerReference{OwnerReference(xtrinode)},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: func() *intstr.IntOrString {
				if minAvailable.IntVal == 0 && minAvailable.StrVal == "" {
					return nil
				}
				return &minAvailable
			}(),
			MaxUnavailable: func() *intstr.IntOrString {
				if maxUnavailable.IntVal == 0 && maxUnavailable.StrVal == "" {
					return nil
				}
				return &maxUnavailable
			}(),
			Selector: &metav1.LabelSelector{
				MatchLabels: TrinoSelectorLabels(xtrinode, ComponentWorker),
			},
		},
	}
}

// coordinatorPDBName returns the name for the coordinator PodDisruptionBudget
func coordinatorPDBName(xtrinode *analyticsv1.XTrinode) string {
	return fmt.Sprintf("%s-coordinator-pdb", xtrinode.Name)
}

// workerPDBName returns the name for the worker PodDisruptionBudget
func workerPDBName(xtrinode *analyticsv1.XTrinode) string {
	return fmt.Sprintf("%s-worker-pdb", xtrinode.Name)
}
