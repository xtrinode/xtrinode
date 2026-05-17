package gracefulshutdown

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	analyticsv1 "github.com/xtrinode/xtrinode/api/v1"
	"github.com/xtrinode/xtrinode/internal/config"
	"github.com/xtrinode/xtrinode/internal/trino/controlauth"
	"github.com/xtrinode/xtrinode/internal/trino/controlendpoint"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// CheckQueriesBeforeScaleDown checks if there are active queries running/queued
// Returns true if safe to scale down (no queries), false if queries are active
// Used for controller-controlled operations (suspend/delete), NOT for KEDA scaling
func CheckQueriesBeforeScaleDown(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, log logr.Logger) (bool, error) {
	// Build the coordinator URL from the generated HTTP service settings.
	coordinatorURL := controlendpoint.CoordinatorURL(xtrinode)

	// Query Trino REST API for active queries
	queryURL := coordinatorURL + config.QueryAPIPath
	return checkQueriesBeforeScaleDown(ctx, cli, xtrinode, queryURL, log)
}

func checkQueriesBeforeScaleDown(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, queryURL string, log logr.Logger) (bool, error) {
	credential, credentialErr := controlauth.CredentialFromXTrinode(ctx, cli, xtrinode)
	if credentialErr != nil {
		return false, fmt.Errorf("failed to load Trino control credential: %w", credentialErr)
	}

	safe, err := checkQueriesBeforeScaleDownURLWithCredential(ctx, queryURL, credential, log)
	if err == nil {
		return safe, nil
	}

	ready, readyErr := coordinatorHasReadyRuntime(ctx, cli, xtrinode)
	if readyErr != nil {
		return false, fmt.Errorf("failed to check coordinator runtime after query API error: %w", readyErr)
	}
	if !ready {
		log.V(1).Info("Coordinator query API unavailable and no ready coordinator runtime exists; assuming safe to scale down", "error", err)
		return true, nil
	}

	log.V(1).Info("Coordinator query API unavailable while coordinator runtime is ready; delaying scale down", "error", err)
	return false, err
}

func checkQueriesBeforeScaleDownURLWithCredential(ctx context.Context, queryURL string, credential controlauth.Credential, log logr.Logger) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", queryURL, http.NoBody)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}
	controlauth.ApplyRequestAuth(req, credential)

	httpClient := &http.Client{Timeout: config.HTTPClientTimeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		log.V(1).Info("Failed to query coordinator, delaying scale down", "error", err)
		return false, fmt.Errorf("failed to query coordinator: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// A responding coordinator with an error status is not the same as a
		// scaled-to-zero coordinator. Fail closed so transient API errors do not
		// permit query-killing suspend/delete transitions.
		log.V(1).Info("Failed to query coordinator, delaying scale down", "status", resp.StatusCode)
		return false, fmt.Errorf("coordinator query API returned status %d", resp.StatusCode)
	}

	// Parse JSON response: [{"queryId":"...","state":"RUNNING",...}, ...]
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response: %w", err)
	}

	var queries []map[string]interface{}
	if err := json.Unmarshal(body, &queries); err != nil {
		return false, fmt.Errorf("failed to parse response: %w", err)
	}

	// Count active queries (state: QUEUED or RUNNING)
	activeCount := 0
	for _, query := range queries {
		if state, ok := query["state"].(string); ok {
			if state == "QUEUED" || state == "RUNNING" {
				activeCount++
			}
		}
	}

	if activeCount > 0 {
		log.Info("Active queries detected, cannot scale down", "activeQueries", activeCount)
		return false, nil
	}

	log.V(1).Info("No active queries, safe to scale down")
	return true, nil
}

func coordinatorHasReadyRuntime(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode) (bool, error) {
	coordinator := &appsv1.Deployment{}
	err := cli.Get(ctx, types.NamespacedName{
		Name:      config.BuildCoordinatorDeploymentName(xtrinode.Name),
		Namespace: xtrinode.Namespace,
	}, coordinator)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return false, err
		}
	} else if coordinator.Status.ReadyReplicas > 0 || coordinator.Status.AvailableReplicas > 0 {
		return true, nil
	}

	pods := &corev1.PodList{}
	if err := cli.List(ctx, pods, &client.ListOptions{
		Namespace:     xtrinode.Namespace,
		LabelSelector: trinoComponentSelector(xtrinode.Name, "coordinator"),
	}); err != nil {
		return false, err
	}

	for i := range pods.Items {
		if podReady(&pods.Items[i]) {
			return true, nil
		}
	}

	return false, nil
}

// WaitForPodTermination waits for pods to finish terminating gracefully
// Used during finalization (delete), NOT for KEDA scaling
func WaitForPodTermination(ctx context.Context, cli client.Client, xtrinode *analyticsv1.XTrinode, log logr.Logger) error {
	// List worker pods
	pods := &corev1.PodList{}
	labelSelector := trinoComponentSelector(xtrinode.Name, "worker")

	if err := cli.List(ctx, pods, &client.ListOptions{
		Namespace:     xtrinode.Namespace,
		LabelSelector: labelSelector,
	}); err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	// Check if any pods are terminating
	for i := range pods.Items {
		if pods.Items[i].DeletionTimestamp != nil {
			// Pod is terminating, check if still within grace period
			gracePeriod := time.Duration(config.DefaultWorkerGracePeriodSeconds) * time.Second
			if pods.Items[i].Spec.TerminationGracePeriodSeconds != nil {
				gracePeriod = time.Duration(*pods.Items[i].Spec.TerminationGracePeriodSeconds) * time.Second
			}

			elapsed := time.Since(pods.Items[i].DeletionTimestamp.Time)
			if elapsed < gracePeriod {
				log.Info("Pod still terminating within grace period", "pod", pods.Items[i].Name, "elapsed", elapsed, "gracePeriod", gracePeriod)
				return fmt.Errorf("pod %s still terminating (elapsed: %v, grace: %v)", pods.Items[i].Name, elapsed, gracePeriod)
			}
		}
	}

	log.V(1).Info("All pods finished terminating")
	return nil
}

func trinoComponentSelector(xtrinodeName, component string) labels.Selector {
	return labels.SelectorFromSet(map[string]string{
		"app.kubernetes.io/name":       "trino",
		"app.kubernetes.io/instance":   xtrinodeName,
		"app.kubernetes.io/managed-by": "xtrinode-operator",
		"app.kubernetes.io/component":  component,
	})
}

func podReady(pod *corev1.Pod) bool {
	if pod == nil || pod.DeletionTimestamp != nil {
		return false
	}
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
