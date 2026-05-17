# CAPI/CAPG Bootstrap

This folder is for Cluster API Provider GCP smoke testing. Terraform owns the management-plane install:

- cert-manager
- Cluster API Operator
- CAPI core, kubeadm bootstrap, and kubeadm control-plane providers
- CAPG with the GKE feature gate enabled

The default Terraform path creates a CAPG Google service account key and writes it to a Kubernetes
Secret. That is convenient for testing, but the key is stored in Terraform state. For a tighter setup,
set `capg_manage_gcp_credentials=false` and pass `capg_gcp_credentials_b64` from an externally
managed service account key.

## Bootstrap CAPG

```bash
scripts/capi/bootstrap.sh
```

This runs:

```bash
terraform -chdir=terraform/gcp apply -var='capg_enabled=true' -target='<CAPG resources>'
```

The targeted apply is intentional here: it installs only the CAPG management-plane resources and
avoids reconciling unrelated drift in optional resources such as Postgres, Iceberg GCS, or the
autoscaled GKE node count.

After it completes, verify:

```bash
kubectl get pods -n capi-operator-system
kubectl get pods -n capi-system
kubectl get pods -n capg-system
kubectl api-resources | rg 'cluster.x-k8s.io|infrastructure.cluster.x-k8s.io'
```

## Create A CAPG Workload GKE Cluster

This is optional and separate from the Terraform-created management GKE cluster:

```bash
scripts/capi/create-cluster.sh
```

The script installs `clusterctl` into the user cache when it is not on `PATH`.
Set `XTRINODE_TOOL_CACHE_DIR` to override that location. It uses Terraform outputs
or `terraform.tfvars` for GCP settings, creates the target namespace, runs a server-side
dry-run, applies the generated manifest, and patches in the existing Terraform subnet
for manual-mode VPCs.

It runs:

```bash
clusterctl generate cluster "$CLUSTER_NAME" \
  --target-namespace "$TARGET_NAMESPACE" \
  --flavor gke \
  --infrastructure "gcp:$CAPG_VERSION"
```

Defaults:

- `CLUSTER_NAME=xtrinode-capg-workload`
- `TARGET_NAMESPACE=xtrinode-capg-real`
- `WORKER_MACHINE_COUNT=0`
- `CAPG_VERSION=v1.11.1`
- `GCP_NETWORK_NAME=xtrinode-network`
- `GCP_SUBNET_NAME=xtrinode-subnet`
- `WAIT_FOR_CLUSTER=true`

For manual-mode VPCs, `GCP_SUBNET_NAME` must point to the existing subnet. Terraform
currently creates `xtrinode-subnet`; without this CAPG reaches GKE and fails with:
`Network "..." uses manual subnet mode and requires specifying a subnetwork`.

## XTrinode Nodepool Tests

The XTrinode operator nodepool reconciler creates `MachineDeployment` or `MachinePool` resources in
the same management cluster where the `XTrinode` CR is reconciled. For GKE managed nodepools, CAPG
must be installed with `GKE=true`, and the target namespace must contain a matching CAPI `Cluster`
resource for `spec.nodePool.clusterName`.

After the CAPG workload cluster is Available, run:

```bash
scripts/capi/test-xtrinode-nodepool.sh
```

Defaults:

- `TARGET_NAMESPACE=xtrinode-capg-real`
- `CLUSTER_NAME=xtrinode-capg-workload`
- `XTRINODE_NAME=capg-nodepool`
- `NODEPOOL_NAME=np-capg-real`
- `MIN_NODES=1`
- `MAX_NODES=1`
- `WAIT_FOR_NODEPOOL=true`
- `WAIT_FOR_TRINO_ROLLOUT=false`
- `XTRINODE_SUSPENDED=true` when `WAIT_FOR_TRINO_ROLLOUT=false`
- `NODEPOOL_SCALE_DOWN_ON_SUSPEND=false`

This creates a real `GCPManagedMachinePool` through the XTrinode operator and waits
for `.status.ready=true` on the provider object. By default the smoke XTrinode stays
suspended and sets `spec.nodePool.scaleDownOnSuspend=false`, so it exercises nodepool
reconciliation without scheduling Trino pods on the management cluster. It can still
create a billable GKE node pool while it is running.

Set `WAIT_FOR_TRINO_ROLLOUT=true` only when you intentionally want to validate a Trino
rollout in the same cluster where the operator is running.
