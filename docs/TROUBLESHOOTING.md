# XTrinode Troubleshooting Guide

## XTrinode Stuck in Reconciling

**Symptoms**: XTrinode status phase remains "Reconciling" for extended time

**Diagnosis**:

```bash
kubectl get xtrinode <runtime> -n <namespace> -o wide
kubectl describe xtrinode <runtime> -n <namespace>
kubectl logs -n xtrinode-system -l app.kubernetes.io/name=xtrinode-operator -f
kubectl get events -n <namespace> --sort-by='.lastTimestamp'
```

**Solutions**:

- Check operator logs for errors
- Verify CRDs and generated manifests are installed
- Check KEDA is running if autoscaling is enabled
- Verify CAPI provider is installed and healthy

## Workers Not Scaling Up

**Symptoms**: Worker replicas remain at 0 even with queued queries

**Diagnosis**:

```bash
kubectl get scaledobject -n <namespace>
kubectl describe scaledobject trino-<runtime>-workers -n <namespace>
kubectl get hpa -n <namespace>
kubectl logs -n xtrinode-system -l app.kubernetes.io/name=keda-operator -f
```

**Solutions**:

- Verify the configured HTTP scaler endpoint or Prometheus query returns data
- Check KEDA ScaledObject status and conditions
- Check node capacity: `kubectl top nodes`
- Verify KEDA trigger threshold is correct

## Query Hangs / 503 on First Request

**Symptoms**: Query returns 503 Service Unavailable or hangs

**Diagnosis**:

```bash
kubectl get xtrinode <runtime> -n <namespace> -o wide
kubectl logs -n xtrinode-gateway -l app.kubernetes.io/name=xtrinode-gateway -f
kubectl get pod -n <namespace> \
  -l app.kubernetes.io/name=trino,app.kubernetes.io/instance=<runtime>,app.kubernetes.io/component=coordinator
kubectl get configmap trino-gateway-routes -n xtrinode-gateway -o yaml
```

**Solutions**:

- Check if XTrinode is suspended: `kubectl get xtrinode <runtime> -n <namespace>`
- If suspended, resume it: `kubectl patch xtrinode <runtime> -n <namespace> --type merge -p '{"spec":{"suspended":false}}'`
- Wait for coordinator to be ready
- Check gateway logs for routing errors
- Verify the `X-Trino-XTrinode` header, hostname, or default route matches the runtime route

## Coordinator Pod Crashes

**Symptoms**: Coordinator pod in CrashLoopBackOff

**Diagnosis**:

```bash
kubectl logs -n <namespace> \
  -l app.kubernetes.io/name=trino,app.kubernetes.io/instance=<runtime>,app.kubernetes.io/component=coordinator \
  --previous
kubectl describe pod -n <namespace> \
  -l app.kubernetes.io/name=trino,app.kubernetes.io/instance=<runtime>,app.kubernetes.io/component=coordinator
```

**Solutions**:

- Check resource limits vs actual usage
- Verify catalog configurations are correct
- Check Trino configuration for syntax errors
- Increase resource requests/limits if needed

## Nodes Not Scaling Up

**Symptoms**: Worker pods unschedulable, nodes not created

**Diagnosis**:

```bash
kubectl get machinepool -A -l xtrinode.analytics.xtrinode.io/runtime=<runtime>
kubectl describe machinepool <runtime>-pool -n <namespace>
kubectl get pods -n <namespace> -o wide
kubectl get events -n <namespace> --sort-by='.lastTimestamp'
```

**Solutions**:

- On GKE, check node auto-provisioning/node-pool status with `gcloud container node-pools list`
- On self-managed autoscaler installs, verify Cluster Autoscaler is running and review its logs
- Check node pool min/max settings
- Verify cloud provider credentials are correct
- Check cloud provider quotas/limits
- Check events for `FailedScheduling`, quota, or provider provisioning errors

## High Memory Usage

**Symptoms**: Queries fail with OutOfMemory errors

**Solutions**:

- Increase worker resource limits in XTrinode spec
- Reduce maxWorkers to allow more memory per worker
- Enable spill to disk (requires local storage)
- Optimize queries to use less memory

## Gateway Route Not Working

**Symptoms**: Queries fail with "no backend available"

**Diagnosis**:

```bash
kubectl logs -n xtrinode-gateway -l app.kubernetes.io/name=xtrinode-gateway -f
kubectl get configmap trino-gateway-routes -n xtrinode-gateway -o yaml
kubectl get service -n <namespace> \
  -l app.kubernetes.io/name=trino,app.kubernetes.io/instance=<runtime>,app.kubernetes.io/component=coordinator
kubectl get endpoints trino-<runtime> -n <namespace>
```

**Solutions**:

- Verify coordinator service exists and is ready
- Check gateway route registration in `trino-gateway-routes`
- Verify the `X-Trino-XTrinode` header, hostname, or default route matches the routing rule
- Check gateway logs for routing errors

## Runtime Deletion Stuck Draining

**Symptoms**: A deleted XTrinode keeps its finalizer and the Gateway route shows the backend as
`DRAINING`.

**Diagnosis**:

```bash
kubectl describe xtrinode <runtime> -n <namespace>
kubectl get configmap trino-gateway-routes -n xtrinode-gateway -o yaml
kubectl logs -n xtrinode-system -l app.kubernetes.io/name=xtrinode-operator -f

# If the coordinator is still reachable, inspect Trino's query endpoint.
kubectl run trino-query-check -n <namespace> --rm -it --image=curlimages/curl -- \
  curl -sS http://trino-<runtime>.<namespace>.svc.cluster.local:8080/v1/query \
  -H 'X-Trino-User: xtrinode-operator'
```

**Behavior**:

- The operator marks the Gateway backend `DRAINING` so new queries are not selected.
- Each reconcile checks the coordinator `/v1/query` endpoint. If no `QUEUED` or `RUNNING` queries
  remain, cleanup can start before the fallback window ends.
- If the query endpoint is unavailable, the operator waits for the configured drain window
  (`xtrinode-operator.operator.lifecycle.drainDuration`, `5m` by default) and then uses that
  elapsed time as the fallback.
- If the query endpoint reports active queries, the operator keeps waiting rather than removing the
  backend.
- The query-aware recheck interval is configured with
  `xtrinode-operator.operator.lifecycle.drainRequeueInterval` and defaults to `30s`.
- Drain progress is tracked on the XTrinode with `drain-started-at`, `drain-completed-at`, and
  `drain-result` annotations under the `xtrinode.analytics.xtrinode.io/` prefix.

**Useful metrics**:

- `xtrinode_drain_active{namespace,name}` shows runtimes currently draining.
- `xtrinode_drain_duration_seconds{namespace,name,result}` records completed drain duration. The
  `result` label is `query_complete` or `time_fallback`.
- `xtrinode_drain_failures_total{namespace,name,reason}` records failed drain starts, bad
  annotations, or query-check failures.
- `xtrinode_api_k8s_lease_acquired_total`, `xtrinode_api_k8s_lease_gated_total`, and
  `xtrinode_api_k8s_lease_errors_total` show API server lifecycle gate outcomes.

**Emergency override policy**:

Do not hand-edit the shared Gateway route ConfigMap during ordinary operations. Any future manual
state override must be an explicit emergency-only path with admin authentication, Kubernetes events
or equivalent audit records, a clear target runtime, bounded cleanup or expiry behavior, and
operator-owned reconciliation back to the normal state. Capture `kubectl describe`, events, and
operator logs before considering a break-glass action.

## KEDA ScaledObject Not Triggering

**Symptoms**: KEDA shows "unknown" status or not scaling

**Diagnosis**:

```bash
kubectl get scaledobject -n <namespace>
kubectl describe scaledobject trino-<runtime>-workers -n <namespace>
kubectl logs -n xtrinode-system -l app.kubernetes.io/name=keda-operator -f
```

**Solutions**:

- Verify Prometheus is accessible from KEDA
- Check Prometheus query returns data
- Verify KEDA trigger configuration
- Check KEDA operator logs for errors

## Resource Quota Exceeded

**Symptoms**: Pod creation fails with "exceeded quota"

**Solutions**:

- Check namespace ResourceQuota: `kubectl describe quota -n <namespace>`
- Reduce maxWorkers or worker resource requests
- Request quota increase from platform team
- Delete unused XTrinodes to free resources

## Common Commands

### Check GKE Platform Health

```bash
kubectl config current-context
kubectl get nodes -o wide
kubectl get pods -A
kubectl get deployments -A
helm list -A
kubectl get events -A --field-selector type=Warning --sort-by='.lastTimestamp'
```

### Check XTrinode Installation

```bash
kubectl get crd xtrinodes.analytics.xtrinode.io xtrinodecatalogs.analytics.xtrinode.io
kubectl get crd scaledobjects.keda.sh scaledjobs.keda.sh
kubectl get deployment -n xtrinode-system
kubectl get deployment -n xtrinode-gateway -l app.kubernetes.io/name=xtrinode-gateway
kubectl get configmap trino-gateway-routes -n xtrinode-gateway -o yaml
kubectl get xtrinode -A
kubectl get scaledobject -A
```

### Check Overall Health

```bash
# Operator
kubectl get deployment -n xtrinode-system
kubectl logs -n xtrinode-system -l app.kubernetes.io/name=xtrinode-operator -f

# KEDA
kubectl get deployment -n xtrinode-system -l app.kubernetes.io/name=keda-operator
kubectl logs -n xtrinode-system -l app.kubernetes.io/name=keda-operator -f

# Prometheus
kubectl get statefulset -n monitoring
kubectl logs -n monitoring -l app.kubernetes.io/name=prometheus -f

# Trino Gateway
kubectl get deployment -n xtrinode-gateway -l app.kubernetes.io/name=xtrinode-gateway
kubectl logs -n xtrinode-gateway -l app.kubernetes.io/name=xtrinode-gateway -f
```

### Debug an XTrinode

```bash
# Full status
kubectl get xtrinode <runtime> -n <namespace> -o yaml

# Events
kubectl describe xtrinode <runtime> -n <namespace>

# Coordinator
kubectl get pod -n <namespace> \
  -l app.kubernetes.io/name=trino,app.kubernetes.io/instance=<runtime>,app.kubernetes.io/component=coordinator
kubectl logs -n <namespace> \
  -l app.kubernetes.io/name=trino,app.kubernetes.io/instance=<runtime>,app.kubernetes.io/component=coordinator

# Workers
kubectl get pod -n <namespace> \
  -l app.kubernetes.io/name=trino,app.kubernetes.io/instance=<runtime>,app.kubernetes.io/component=worker
kubectl logs -n <namespace> \
  -l app.kubernetes.io/name=trino,app.kubernetes.io/instance=<runtime>,app.kubernetes.io/component=worker

# KEDA
kubectl get scaledobject -n <namespace>
kubectl describe scaledobject trino-<runtime>-workers -n <namespace>

# Node pool
kubectl get machinepool -A -l xtrinode.analytics.xtrinode.io/runtime=<runtime>
kubectl get nodes --show-labels
```

## Getting Help

1. Check logs: `kubectl logs -n xtrinode-system -l app.kubernetes.io/name=xtrinode-operator -f`
2. Describe resources: `kubectl describe xtrinode <runtime> -n <namespace>`
3. Check events: `kubectl get events -n <namespace> --sort-by='.lastTimestamp'`
4. Review metrics in Prometheus
5. Contact platform team with logs and resource descriptions
