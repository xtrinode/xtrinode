package resources

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
)

// CleanupOldConfigMapRevisions removes old revisioned ConfigMaps, keeping only the last N revisions
// CleanupOldConfigMapRevisions prevents ConfigMap accumulation over time.
func CleanupOldConfigMapRevisions(
	ctx context.Context,
	c client.Client,
	xtrinode *analyticsv1.XTrinode,
	currentRevision string,
) error {
	// List all ConfigMaps owned by this XTrinode
	configMapList := &corev1.ConfigMapList{}
	listOpts := &client.ListOptions{
		Namespace: xtrinode.Namespace,
		LabelSelector: labels.SelectorFromSet(labels.Set{
			"app.kubernetes.io/name":       "trino",
			"app.kubernetes.io/instance":   xtrinode.Name,
			"app.kubernetes.io/managed-by": "xtrinode-operator",
		}),
	}

	if err := c.List(ctx, configMapList, listOpts); err != nil {
		return fmt.Errorf("failed to list ConfigMaps: %w", err)
	}

	// Group ConfigMaps by type (coordinator vs worker)
	coordinatorCMs := make(map[string]*corev1.ConfigMap)
	workerCMs := make(map[string]*corev1.ConfigMap)

	coordinatorPrefix := fmt.Sprintf("trino-%s-coordinator-", xtrinode.Name)
	workerPrefix := fmt.Sprintf("trino-%s-worker-", xtrinode.Name)

	for i := range configMapList.Items {
		cm := &configMapList.Items[i]
		name := cm.Name

		if strings.HasPrefix(name, coordinatorPrefix) {
			// Extract revision from name
			revision := strings.TrimPrefix(name, coordinatorPrefix)
			if revision != "" && revision != currentRevision {
				coordinatorCMs[revision] = cm
			}
		} else if strings.HasPrefix(name, workerPrefix) {
			// Extract revision from name
			revision := strings.TrimPrefix(name, workerPrefix)
			if revision != "" && revision != currentRevision {
				workerCMs[revision] = cm
			}
		}
	}

	// Clean up old coordinator ConfigMaps
	if err := cleanupOldRevisions(ctx, c, coordinatorCMs, config.MaxRevisionHistory); err != nil {
		return fmt.Errorf("failed to cleanup coordinator ConfigMaps: %w", err)
	}

	// Clean up old worker ConfigMaps
	if err := cleanupOldRevisions(ctx, c, workerCMs, config.MaxRevisionHistory); err != nil {
		return fmt.Errorf("failed to cleanup worker ConfigMaps: %w", err)
	}

	return nil
}

// cleanupOldRevisions deletes old ConfigMap revisions, keeping only the last N
func cleanupOldRevisions(
	ctx context.Context,
	c client.Client,
	revisionMap map[string]*corev1.ConfigMap,
	keepCount int,
) error {
	if len(revisionMap) <= keepCount {
		// Nothing to clean up
		return nil
	}

	// Sort revisions by creation timestamp (oldest first)
	type revisionInfo struct {
		revision string
		cm       *corev1.ConfigMap
	}

	revisions := make([]revisionInfo, 0, len(revisionMap))
	for rev, cm := range revisionMap {
		revisions = append(revisions, revisionInfo{
			revision: rev,
			cm:       cm,
		})
	}

	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].cm.CreationTimestamp.Before(&revisions[j].cm.CreationTimestamp)
	})

	// Delete oldest revisions, keeping only the last N
	deleteCount := len(revisions) - keepCount
	for i := 0; i < deleteCount; i++ {
		cm := revisions[i].cm
		if err := c.Delete(ctx, cm); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete old ConfigMap %s: %w", cm.Name, err)
		}
	}

	return nil
}
