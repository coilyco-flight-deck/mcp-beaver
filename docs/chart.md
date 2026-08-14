# The ward-mcp Helm chart

The auth-neutral distribution vehicle ward-mcp ships (ward-mcp#6). **One
chart, one runtime image, many releases.** The chart can mount a `.mcp.kdl`
guardfile or wrap an existing streamable-HTTP MCP with an exact tool allowlist.
Adding an MCP runtime becomes a values file plus `helm upgrade`.

```sh
helm upgrade --install skillsmp ward-mcp \
  -f skillsmp.values.yaml \
  --set-file spec=skillsmp.mcp.kdl \
  --set image.tag=<built-runtime-sha>
```

The chart lives here because the product **distributes as image + chart** (ward-mcp#6). It stays generic and spec-opaque: it takes the `.mcp.kdl` as an opaque blob and never parses it, so the [interior-only scope](DESIGN.md) of the spec is preserved. The spec never reaches down into a chart, and the chart never reaches up into the spec. [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) composes this runtime contract with its fleet-specific exposure charts and owns rollout.

For an upstream MCP:

```sh
helm upgrade --install reader ward-mcp \
  -f upstream.values.yaml \
  --set image.tag=<built-runtime-sha>
```

## What it templates

* `configmap-spec.yaml` renders only in spec mode. It mounts the `.mcp.kdl` at
  `/spec/<name>.mcp.kdl`.
* `deployment.yaml` renders the generic runtime in spec or upstream mode,
  injects application tokens when requested, and appends optional co-located
  containers.
* `service.yaml` renders a ClusterIP, or a NodePort when `service.nodePort` is set.
* `externalsecret.yaml` pulls each SSM-path `secret` entry into the environment variable named by the guardfile.

The chart intentionally renders no Ingress, authentication proxy, identity-provider integration, certificate, DNS record, or RFC 9728 metadata endpoint. The consuming deployment owns those concerns.

## The runtime contract this chart targets

The chart is buildable against the pinned runtime contract below, in parallel with the runtime image that implements it. Finalize the mount path / port / probe once the runtime lands.

- spec mode runs `ward-mcp serve /spec/<name>.mcp.kdl --http :8080`
- upstream mode runs `ward-mcp serve-upstream --upstream <url> --tool <name>... --connect-timeout 2m --http :8080`
- MCP served over SSE / streamable-HTTP at `/mcp`
- every projected tool automatically served at `POST /api/{tool-name}` on the
  same listener
- application secrets remain server-side
- the `.mcp.kdl` is read from the mounted path in spec mode, never baked

## Values reference

### The spec

- **`runtime.mode`** - `spec` by default, or `upstream`.
- **`runtime.injectSecrets`** - inject generated application secrets into the
  ward-mcp container. Disable this when only a co-located upstream needs them.
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

## Sidecar shape: wrapping a co-located non-MCP process

`extraContainers` works in **both** modes. The chart appends it to the pod's
container list with no mode condition; only the runtime args, the `/spec`
volumeMount, and the volumes are mode-conditional. Only upstream mode was ever
documented, which made a supported shape look unavailable.

The two modes co-locate for different reasons:

- **Upstream mode** wraps a sidecar that already speaks MCP. `serve-upstream`
  snapshots its tool list at startup and proxies an allowlist.
- **Spec mode** wraps a sidecar that speaks **plain HTTP JSON**. There is no
  upstream MCP to snapshot; `base-url` simply points at `127.0.0.1`, because
  containers in a pod share a network namespace. This is the shape for serving
  a bundled dataset, and it removes a whole class of external dependency: no
  third-party rate limit, no third-party uptime, no per-request caching
  problem. `restrict` still bounds the surface exactly as it would remotely.

Reference: [`examples/sidecar.mcp.kdl`](../examples/sidecar.mcp.kdl) with
[`examples/sidecar.values.yaml`](../examples/sidecar.values.yaml), rendered by
`ward exec helm-template-sidecar`.

### Readiness is the one real decision

Spec mode never connects at startup - it resolves `base-url` per request - so
unlike upstream mode there is **no crash-loop risk** from a slow sidecar. The
cost is the opposite failure: ward-mcp binds immediately and answers
`tools/list` correctly while the sidecar is still loading, so the pod reports
Ready with its data plane down and takes traffic that errors.

The chart's default `readinessProbe` checks ward-mcp's own port, which is right
for upstream mode and wrong for this one. The reference values gate readiness
on the sidecar's port instead, which a probe may target directly because the
containers share a namespace. Liveness stays on ward-mcp: a wedged sidecar
should fail readiness and stop taking traffic, not restart the pod out from
under a healthy runtime.

Accepting the default and letting early requests fail is defensible for a
sidecar that starts fast. It is not the default here, because "fails briefly
after every rollout" is the kind of error that gets attributed to the wrong
component.

## Exposure belongs to the consumer

The product chart does not choose between public, private, or tailnet access. It also carries no fleet identity provider or ingress-controller assumptions. The MCP and automatic HTTP tool API share that boundary and receive no runtime-owned inbound authentication. CoilyCo deployments that need a public authenticated surface use deploy's `charts/ingress-public-authed` chart. Other consumers bring an equivalent composition for their environment.

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
