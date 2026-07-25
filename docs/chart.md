# The ward-mcp Helm chart

The auth-neutral distribution vehicle ward-mcp ships (ward-mcp#6). **One chart, one runtime image, many releases:** the `.mcp.kdl` guardfile rides in as chart values (a ConfigMap), and the chart templates the runtime workload and Service. Adding an MCP runtime becomes a values file plus `helm upgrade` - no per-guardfile image build, no per-service manifest fork.

```sh
helm upgrade --install skillsmp ward-mcp \
  -f skillsmp.values.yaml \
  --set-file spec=skillsmp.mcp.kdl \
  --set image.tag=<built-runtime-sha>
```

The chart lives here because the product **distributes as image + chart** (ward-mcp#6). It stays generic and spec-opaque: it takes the `.mcp.kdl` as an opaque blob and never parses it, so the [interior-only scope](DESIGN.md) of the spec is preserved. The spec never reaches down into a chart, and the chart never reaches up into the spec. [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) composes this runtime contract with its fleet-specific exposure charts and owns rollout.

## What it templates

* `configmap-spec.yaml` renders the `.mcp.kdl` from `--set-file spec=...` and mounts it at `/spec/<name>.mcp.kdl`.
* `deployment.yaml` renders the generic runtime and injects application tokens as environment variables.
* `service.yaml` renders a ClusterIP, or a NodePort when `service.nodePort` is set.
* `externalsecret.yaml` pulls each SSM-path `secret` entry into the environment variable named by the guardfile.

The chart intentionally renders no Ingress, authentication proxy, identity-provider integration, certificate, DNS record, or RFC 9728 metadata endpoint. The consuming deployment owns those concerns.

## The runtime contract this chart targets

The chart is buildable against the pinned runtime contract below, in parallel with the runtime image that implements it. Finalize the mount path / port / probe once the runtime lands.

- image ENTRYPOINT `ward-mcp serve /spec/<name>.mcp.kdl --http :8080` (the chart supplies the args, so the path and port stay chart-controlled)
- MCP served over SSE / streamable-HTTP at `/mcp`
- the token supplied as env, its name per the guardfile's `value env <VAR>`
- the `.mcp.kdl` read from the mounted path, not baked into the image

## Values reference

### The spec

- **`spec`** - the `.mcp.kdl` guardfile body, supplied with `--set-file spec=<name>.mcp.kdl`. Written to a ConfigMap, mounted at `/spec/<specName>.mcp.kdl`. Empty by default so a bare render fails loud rather than shipping an MCP with no surface.
- **`specName`** - the `<name>` in the mount path. Defaults to the release name.

### Image

- **`image.repository`** - defaults to the public Forgejo path; a deploy overrides it to the in-cluster registry mirror k3s pulls without auth (kept out of this public-safe repo).
- **`image.tag`** - defaults to `.Chart.appVersion`; a rollout sets it to the built runtime sha. The guardfile it serves is values, not a per-server image.
- **`image.pullPolicy`** - `Always` by default (the shared `:latest`-style runtime).

### Secret wiring

- **`secret`** - a map of `ENV_VAR -> source`, one entry per `value env <VAR>` the guardfile names. Two source forms:
  - a **string** is an SSM parameter path - the chart mints an ExternalSecret pulling it into a Secret and injects it as that env var (`SKILLSMP_API_KEY: /skillsmp/api-key`).
  - a **map** `{secretName, key}` references an already-existing Secret instead of minting one.
- **`externalSecret.refreshInterval` / `.secretStoreRef`** - the store the SSM-path entries resolve through (`aws-parameter-store` ClusterSecretStore by default).

### Service

- **`service.type`** - `ClusterIP` by default.
- **`service.nodePort`** - set it to bind an optional direct node port. Unset leaves the Service ClusterIP-only. The consuming deployment chooses and protects any reachable node surface.

### Pod

- **`replicaCount`, `resources`, `nodeSelector`, `tolerations`, `affinity`, `podSecurityContext`, `securityContext`, `readinessProbe`, `livenessProbe`, `extraEnv`** - the usual pod knobs. Scheduling is empty by default and belongs to the consuming deployment.

## Exposure belongs to the consumer

The product chart does not choose between public, private, or tailnet access. It also carries no fleet identity provider or ingress-controller assumptions. CoilyCo deployments that need a public authenticated MCP use deploy's `charts/ingress-public-authed` chart. Other consumers bring an equivalent composition for their environment.

## Verify

Run the tracked Ward verbs:

* `ward exec helm-lint-chart`
* `ward exec helm-template-clusterip`
* `ward exec helm-template-nodeport`

The two renders prove that the chart emits the runtime objects for both Service shapes. Exposure-layer verification belongs to the consuming deployment.
