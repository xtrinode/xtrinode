# XTrinode Umbrella Chart

This is the **umbrella/parent chart** that manages all XTrinode components:

- **xtrinode-operator** - Controller for managing XTrinode resources
- **xtrinode-api-server** - REST API for runtime management
- **xtrinode-gateway** - Query routing gateway

## Quick Start

### Deploy All Components

```bash
# Deploy everything
helm install xtrinode ./helm/xtrinode \
  --namespace xtrinode-system \
  --create-namespace

# Or with Gateway ingress for Trino clients. The API server remains internal.
helm install xtrinode ./helm/xtrinode \
  --namespace xtrinode-system \
  --create-namespace \
  --set xtrinode-gateway.ingress.enabled=true \
  --set xtrinode-gateway.ingress.hosts[0].host=gateway.example.com
```

### Deploy Selected Components

```bash
# Deploy only operator and API server (no gateway)
helm install xtrinode ./helm/xtrinode \
  --namespace xtrinode-system \
  --create-namespace \
  --set xtrinode-gateway.enabled=false

# Deploy only operator
helm install xtrinode ./helm/xtrinode \
  --namespace xtrinode-system \
  --create-namespace \
  --set xtrinode-api-server.enabled=false \
  --set xtrinode-gateway.enabled=false
```

## Component Enable/Disable

Each component can be enabled or disabled using the `enabled` flag:

```yaml
xtrinode-operator:
  enabled: true   # Deploy operator

xtrinode-api-server:
  enabled: true   # Deploy API server

xtrinode-gateway:
  enabled: true   # Deploy gateway
```

## Configuration

All component configurations are nested under their component name:

```yaml
# API Server configuration. Keep this service internal; use Gateway ingress for
# Trino client traffic.
xtrinode-api-server:
  replicaCount: 2
  apiServer:
    port: 8081
    apiPath: "/api/v1"
    auth:
      enabled: true
      existingSecret: xtrinode-api-server-auth
      resume:
        enabled: true
        existingSecret: xtrinode-api-server-resume-auth
  ingress:
    enabled: false

# Gateway configuration
xtrinode-gateway:
  replicaCount: 2
  gateway:
    apiServerURL: "http://xtrinode-api-server.xtrinode-system.svc.cluster.local:8081/api/v1"
    apiServerAuth:
      enabled: true
      existingSecret: xtrinode-api-server-resume-auth
    redis:
      enabled: true
      url: "redis://redis.example:6379/0"
  ingress:
    enabled: true
    hosts:
      - host: gateway.example.com
```

### API Server Authentication And Authorization

The API server is an internal control-plane service for operator, Gateway, and trusted automation
calls. It is not a tenant-facing or direct end-user API. API server control-plane endpoints under
`/api/v1` support bearer-token authentication. Health and Prometheus `/metrics` endpoints remain
unauthenticated for probes and scraping.

When enabling it, configure an admin token for direct API access and a separate resume-only token
for the gateway. In production, prefer pre-created Secrets in each component namespace and use
`existingSecret`:

```yaml
xtrinode-api-server:
  apiServer:
    auth:
      enabled: true
      existingSecret: xtrinode-api-server-auth
      resume:
        enabled: true
        existingSecret: xtrinode-api-server-resume-auth

xtrinode-gateway:
  gateway:
    apiServerAuth:
      enabled: true
      existingSecret: xtrinode-api-server-resume-auth
```

The resume-only token can call `/api/v1/resume` and direct runtime resume endpoints, but receives
`403 FORBIDDEN` for read, create, delete, and suspend actions.

Browser CORS is disabled by default; set `xtrinode-api-server.apiServer.cors.allowedOrigins` only
for exact browser origins that should call the control-plane API. Direct browser or public ingress
access is unsupported for tenants and end users because tenant-aware authorization is not
implemented.

If an operator deliberately enables API-server ingress for restricted administrative access, Helm
requires bearer auth, TLS, and exact non-wildcard CORS origins. This does not make the API
tenant-safe; an exposed-mode design must add tenant-aware authorization before direct user access is
supported.

### XTrinode Privileged Overlay And Trino Control Auth

`XTrinode.spec.valuesOverlay` is privileged input. The operator webhook rejects creates or updates
that add/change it, or that add/change `helmChartConfig`, unless the admission user can `update`
`analytics.xtrinode.io/xtrinodes/valuesoverlay` in the target namespace. Tenant roles should not
have that permission.

If you enable Trino HTTP authentication through overlay or chart-aligned config, also configure a
first-class internal lifecycle credential on each `XTrinode`:

```yaml
spec:
  trinoControlAuth:
    username: xtrinode-operator
    passwordSecret:
      name: trino-control-auth
      key: password
  valuesOverlay:
    additionalConfigProperties:
      - internal-communication.shared-secret=<same-strong-secret-on-all-nodes>
```

The Secret must exist in the `XTrinode` namespace and the user must be valid in Trino's password
authenticator. Autosuspend, graceful shutdown, and worker preStop hooks use this credential for
internal Trino API calls and send the forwarded HTTPS header required by Trino when
`http-server.process-forwarded=true`. Trino also requires `internal-communication.shared-secret`
whenever HTTP authentication is enabled, and admission rejects PASSWORD-authenticated specs that omit
it. The built-in lifecycle client currently supports Trino `PASSWORD` authentication; other Trino
HTTP authentication types are rejected by admission until a matching internal control channel is
implemented.

Trino TLS server mode is also rejected for now. XTrinode currently derives gateway and lifecycle
coordinator URLs as HTTP service URLs, and Trino TLS server mode disables that HTTP listener. Use
external TLS termination in front of the gateway until native HTTPS coordinator/control support is
implemented.

Raw config-property overlays may not disable the Trino HTTP listener or override
`http-server.http.port` directly. Use `valuesOverlay.service.port` for supported HTTP port changes
so generated Services, routes, status, autosuspend, and graceful shutdown use the same port.

### Admission Webhook Failure Policy

`xtrinode-operator.webhook.failurePolicy` defaults to `Fail`. Keep that default in production so
privileged overlay authorization, catalog Secret authorization, break-glass checks, defaults, and
runtime-shape validation fail closed when the operator webhook is unreachable.

Setting it to `Ignore` is an emergency availability tradeoff. During that window, the Kubernetes API
server can admit `XTrinode` or `XTrinodeCatalog` changes that the webhook would normally reject or
default. Review all admitted changes and revert to `Fail` after the webhook Service is healthy.

### Control-Plane Node Pool Placement

Keep XTrinode control-plane agents on a small, always-on node pool and keep Trino coordinator and
worker capacity on workload-sized node pools. The chart defaults leave scheduling unconstrained so
local and unlabeled clusters keep working; production installs can opt in with values like these:

```yaml
xtrinode-operator:
  replicaCount: 2
  priorityClassName: xtrinode-control-plane
  nodeSelector:
    xtrinode.io/nodepool: system
  tolerations:
    - key: xtrinode.io/nodepool
      operator: Equal
      value: system
      effect: NoSchedule
  podDisruptionBudget:
    enabled: true
    minAvailable: 1
  keda:
    priorityClassName: xtrinode-control-plane
    nodeSelector:
      xtrinode.io/nodepool: system
    tolerations:
      - key: xtrinode.io/nodepool
        operator: Equal
        value: system
        effect: NoSchedule

xtrinode-api-server:
  replicaCount: 2
  priorityClassName: xtrinode-control-plane
  nodeSelector:
    xtrinode.io/nodepool: system
  tolerations:
    - key: xtrinode.io/nodepool
      operator: Equal
      value: system
      effect: NoSchedule

xtrinode-gateway:
  replicaCount: 2
  priorityClassName: xtrinode-control-plane
  nodeSelector:
    xtrinode.io/nodepool: system
  tolerations:
    - key: xtrinode.io/nodepool
      operator: Equal
      value: system
      effect: NoSchedule
  redis:
    priorityClassName: xtrinode-control-plane
    nodeSelector:
      xtrinode.io/nodepool: system
    tolerations:
      - key: xtrinode.io/nodepool
        operator: Equal
        value: system
        effect: NoSchedule
```

Create the referenced `PriorityClass` once per cluster if you use it:

```yaml
apiVersion: scheduling.k8s.io/v1
kind: PriorityClass
metadata:
  name: xtrinode-control-plane
value: 100000
globalDefault: false
description: XTrinode always-on control-plane components.
```

For dev and small clusters, a system pool with `min=1` is usually enough. For production HA, prefer
`min=2` or `min=3` across zones, run multiple operator/API/gateway replicas, and avoid spot or
preemptible capacity for the operator unless downtime is acceptable. If gateway traffic is large,
move only `xtrinode-gateway` to a dedicated always-on gateway pool. Trino runtime placement remains
runtime-specific through `XTrinode.spec.placement`; when XTrinode provisions the pool,
`XTrinode.spec.nodePool.schedulePods=true` binds coordinator and worker pods to the managed
node-pool label.

## Ingress Configuration

### API Server Internal Boundary

Keep API-server ingress disabled for the supported deployment posture. The API server should be
reachable through its ClusterIP Service from the operator namespace, Gateway namespace, and trusted
automation running on the cluster network.

If you deliberately enable ingress for restricted administrative access, chart rendering requires
all of the following:

- `xtrinode-api-server.apiServer.auth.enabled=true`
- `xtrinode-api-server.apiServer.cors.allowedOrigins` with exact non-wildcard browser origins
- `xtrinode-api-server.ingress.tls` for TLS termination

Tenant-aware authorization is not implemented, so this path is not supported for direct tenant or
end-user access.

```yaml
xtrinode-api-server:
  apiServer:
    auth:
      enabled: true
      existingSecret: xtrinode-api-server-auth
      resume:
        enabled: true
        existingSecret: xtrinode-api-server-resume-auth
    cors:
      allowedOrigins:
        - https://admin.example.com
  ingress:
    enabled: true
    className: "nginx"  # or "traefik", "istio", etc.
    annotations:
      kubernetes.io/ingress.class: nginx
      cert-manager.io/cluster-issuer: "letsencrypt-prod"
    hosts:
      - host: xtrinode-api.example.com
        paths:
          - path: /
            pathType: Prefix
    tls:
      - secretName: xtrinode-api-tls
        hosts:
          - xtrinode-api.example.com
```

### Gateway Ingress

```yaml
xtrinode-gateway:
  ingress:
    enabled: true
    className: "nginx"
    annotations:
      kubernetes.io/ingress.class: nginx
      cert-manager.io/cluster-issuer: "letsencrypt-prod"
    hosts:
      - host: trino-gateway.example.com
        paths:
          - path: /
            pathType: Prefix
    tls:
      - secretName: trino-gateway-tls
        hosts:
          - trino-gateway.example.com
```

### Gateway UI

The Gateway can serve an embedded read-only admin UI at `/ui/admin` and a dynamic status API at
`/ui/admin/api/gateway/status`. This UI shows the Gateway's current routing view, backend
lifecycle states, auto-suspend metadata, health check state, circuit-breaker state, and route reload
status. The summary includes the number of draining backends, and backend details show the drain
deadline and remaining fallback time. Trino's own web UI is exposed per backend at
`/ui/<namespace>/<backend>/`.

The operator's deletion drain timing is configurable through the operator chart values:

```yaml
xtrinode-operator:
  operator:
    lifecycle:
      drainDuration: "5m"
      drainRequeueInterval: "30s"
```

The UI is disabled by default. If it is enabled behind public ingress, the chart requires Gateway
auth and TLS:

```yaml
xtrinode-gateway:
  gateway:
    ui:
      enabled: true
      requireAuth: true
    auth:
      enabled: true
      type: oidc
      oauth:
        issuer: "https://issuer.example"
        audience: "trino-gateway"
  ingress:
    enabled: true
    tls:
      - secretName: trino-gateway-tls
        hosts:
          - trino-gateway.example.com
```

## Examples

### Full Deployment with Gateway Ingress

```bash
helm install xtrinode ./helm/xtrinode \
  --namespace xtrinode-system \
  --create-namespace \
  --set xtrinode-gateway.ingress.enabled=true \
  --set xtrinode-gateway.ingress.className=nginx \
  --set xtrinode-gateway.ingress.hosts[0].host=gateway.example.com
```

### Development (Single Replica, No Ingress)

```bash
helm install xtrinode ./helm/xtrinode \
  --namespace xtrinode-system \
  --create-namespace \
  --set xtrinode-operator.replicaCount=1 \
  --set xtrinode-api-server.replicaCount=1 \
  --set xtrinode-gateway.replicaCount=1 \
  --set xtrinode-api-server.ingress.enabled=false \
  --set xtrinode-gateway.ingress.enabled=false
```

### Production (High Availability)

```bash
helm install xtrinode ./helm/xtrinode \
  --namespace xtrinode-system \
  --create-namespace \
  --set xtrinode-operator.replicaCount=3 \
  --set xtrinode-api-server.replicaCount=3 \
  --set xtrinode-gateway.replicaCount=3 \
  --set xtrinode-api-server.apiServer.auth.enabled=true \
  --set xtrinode-api-server.apiServer.auth.existingSecret=xtrinode-api-server-auth \
  --set xtrinode-api-server.apiServer.auth.resume.enabled=true \
  --set xtrinode-api-server.apiServer.auth.resume.existingSecret=xtrinode-api-server-resume-auth \
  --set xtrinode-gateway.gateway.apiServerAuth.enabled=true \
  --set xtrinode-gateway.gateway.apiServerAuth.existingSecret=xtrinode-api-server-resume-auth \
  --set xtrinode-gateway.gateway.redis.enabled=true \
  --set xtrinode-gateway.gateway.redis.url=redis://redis.example:6379/0 \
  --set xtrinode-api-server.ingress.enabled=false \
  --set xtrinode-gateway.ingress.enabled=true \
  --set global.imageRegistry=your-registry.io
```

## Updating Dependencies

After modifying subchart values, update dependencies:

```bash
cd helm/xtrinode
helm dependency update
```

## Chart Structure

```text
helm/xtrinode/
├── Chart.yaml          # Umbrella chart definition with dependencies
├── values.yaml         # Master values file
└── templates/          # (empty - all templates in subcharts)
```

Subcharts:

- `helm/xtrinode-operator/` - Operator chart
- `helm/xtrinode-api-server/` - API server chart
- `helm/xtrinode-gateway/` - Gateway chart

## Values File Structure

The master `values.yaml` is organized by component:

```yaml
# Global settings
global:
  imageRegistry: yourregistry

# Component enable flags
xtrinode-operator:
  enabled: true
  # ... operator config ...

xtrinode-api-server:
  enabled: true
  # ... API server config ...

xtrinode-gateway:
  enabled: true
  namespaceOverride: xtrinode-gateway
  # ... gateway config ...
```

## Notes

- All components are enabled by default
- Component configurations override their respective subchart defaults
- Use `helm dependency update` after modifying subchart versions
- API-server ingress is disabled by default and should stay internal unless a restricted
  administrative exposure path is deliberately configured.
- Gateway ingress is the supported external entrypoint for Trino clients.
