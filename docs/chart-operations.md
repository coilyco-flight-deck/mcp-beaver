# Chart operations

Upgrading, exposure, and verification for the generic Helm chart. Split from [chart-sidecars.md](chart-sidecars.md).

## Verify

Run the tracked Ward verbs:

* `ward exec helm-lint-chart`
* `ward exec helm-template-clusterip`
* `ward exec helm-template-nodeport`
* `ward exec helm-template-upstream`
* `ward exec helm-template-sidecar`

The renders prove both Service shapes, the allowlisted upstream shape, and the
spec-mode sidecar shape.
Exposure-layer verification belongs to the consuming deployment.

## The runtime contract this chart targets

The chart is buildable against the pinned runtime contract below, in parallel with the runtime image that implements it. Finalize the mount path / port / probe once the runtime lands.

- spec mode runs `mcp-beaver serve /spec/<name>.mcp.kdl --http :8080`
- upstream mode runs `mcp-beaver serve-upstream --upstream <url> --tool <name>... --connect-timeout 2m --http :8080`
- MCP served over SSE / streamable-HTTP at `/mcp`
- every projected tool automatically served at `POST /api/{tool-name}` on the
  same listener
- application secrets remain server-side
- the `.mcp.kdl` is read from the mounted path in spec mode, never baked

## Values reference

### The spec

- **`runtime.mode`** - `spec` by default, or `upstream`.
- **`runtime.injectSecrets`** - inject generated application secrets into the
  mcp-beaver container. Disable this when only a co-located upstream needs them.
- **`spec`** - the `.mcp.kdl` guardfile body in spec mode, supplied with `--set-file spec=<name>.mcp.kdl`. Written to a ConfigMap, mounted at `/spec/<specName>.mcp.kdl`. Empty by default so a spec-mode render fails loud.
- **`specName`** - the `<name>` in the mount path. Defaults to the release name.

### The upstream

- **`upstream.url`** - private streamable-HTTP MCP endpoint. Required in
  upstream mode.
- **`upstream.name`** - outward MCP server name. Defaults to the release
  fullname.
- **`upstream.tools`** - exact tool-name allowlist. At least one entry is
  required.
- **`upstream.connectTimeout`** - bounded retry window for initial connection,
  `2m` by default.
- **`extraContainers`** - optional upstream or support containers appended to
  the pod. A loopback-only upstream keeps its unfiltered surface off the
  network. **Not gated on `runtime.mode`** - see "Sidecar shape" below.

### Image

- **`image.repository`** - defaults to the canonical Forgejo OCI path.
- **`image.tag`** - defaults to `.Chart.appVersion`; a rollout sets it to the built runtime sha. The guardfile it serves is values, not a per-server image.
- **`image.pullPolicy`** - `Always` by default (the shared `:latest`-style runtime).
- **`imagePullSecrets`** - a private-package consumer names the
  `kubernetes.io/dockerconfigjson` Secret backed by its read-only Forgejo
  package credential.

### Secret wiring

- **`secret`** - a map of `ENV_VAR -> source`, one entry per `value env <VAR>` the guardfile names. Two source forms:
  - a **string** is an SSM parameter path - the chart mints an ExternalSecret pulling it into a Secret and injects it as that env var (`SKILLSMP_API_KEY: /skillsmp/api-key`).
  - a **map** `{secretName, key}` references an already-existing Secret instead of minting one.
- **`externalSecret.refreshInterval` / `.secretStoreRef`** - the store the SSM-path entries resolve through (`aws-parameter-store` ClusterSecretStore by default).

### Service

- **`service.type`** - `ClusterIP` by default.
- **`service.nodePort`** - set it to bind an optional direct node port. Unset leaves the Service ClusterIP-only. The consuming deployment chooses and protects any reachable node surface.

### Pod

- **`replicaCount`, `resources`, `nodeSelector`, `tolerations`, `affinity`,
  `podSecurityContext`, `securityContext`, `startupProbe`, `readinessProbe`,
  `livenessProbe`, `extraEnv`** - the usual pod knobs. The startup probe keeps
  upstream warmup from becoming a liveness crash cycle.

### OpenTelemetry

The chart carries no collector endpoint or fleet service identity. A consuming
deployment opts in through `extraEnv` using standard variables:

```yaml
extraEnv:
  - name: OTEL_TRACES_EXPORTER
    value: otlp
  - name: OTEL_METRICS_EXPORTER
    value: otlp
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: https://collector.example.invalid
  - name: OTEL_SERVICE_NAME
    value: reader-mcp
  - name: OTEL_RESOURCE_ATTRIBUTES
    value: deployment.environment.name=example
```

`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` and
`OTEL_EXPORTER_OTLP_METRICS_ENDPOINT` may replace the shared endpoint. Protocol
selection uses `OTEL_EXPORTER_OTLP_PROTOCOL` or its signal-specific standard
variants. `OTEL_SDK_DISABLED=true`, `OTEL_TRACES_EXPORTER=none`, and
`OTEL_METRICS_EXPORTER=none` are honored. Leaving all selectors and endpoints
unset keeps startup a no-network no-op.

## See also

- [chart.md](chart.md) - what the chart templates.
- [chart-sidecars.md](chart-sidecars.md) - the sidecar shape.
