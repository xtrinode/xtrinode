# XTrinode Observability Chart

This chart owns the GCP/local observability stack for XTrinode:

- `prometheus-stack`: `kube-prometheus-stack` dependency for Prometheus Operator, Prometheus, CRDs, and ServiceMonitor scraping.
- `vector`: local `xtrinode-vector` subchart for Kubernetes log collection and Vector self-metrics.

The chart intentionally disables generic cluster infrastructure monitors by default (`nodeExporter`,
kubelet, API server, scheduler, etc.) so it can coexist with an existing cluster monitoring stack.
XTrinode charts render their own `ServiceMonitor` resources when Prometheus is enabled.
The chart follows XTrinode's Kubernetes target and requires Kubernetes 1.32 or newer. Its CRD
upgrade job is enabled by default so existing installs receive Prometheus Operator CRD updates
during chart upgrades.
This chart also renders an optional Grafana dashboard ConfigMap for lifecycle drain state and API
server Lease acquired/gated/error outcomes. It is enabled by default with the
`grafana_dashboard=1` label used by the Grafana sidecar.

The dashboard can be disabled with:

```sh
--set dashboards.enabled=false
```

Install directly:

```sh
helm dependency update helm/xtrinode-observability
helm upgrade --install xtrinode-observability helm/xtrinode-observability \
  --namespace monitoring \
  --create-namespace \
  --set prometheus-stack.enabled=true \
  --set vector.enabled=true
```

For GCP, prefer `scripts/deploy-gcp.sh` or `make gcp-control-plane-deploy`; Terraform installs this same chart through `helm_release.xtrinode_observability`.
