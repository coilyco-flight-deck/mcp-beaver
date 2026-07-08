# The ward-mcp Helm chart

The generic distribution vehicle ward-mcp ships (ward-mcp#6). **One chart, one runtime image, many releases:** the `.mcp.kdl` guardfile rides in as chart values (a ConfigMap), and the chart templates every piece of the k3s exposure. Adding an MCP becomes a values file plus `helm upgrade` - no per-guardfile image build, no per-service manifest fork.

```sh
helm upgrade --install skillsmp ward-mcp \
  -f skillsmp.values.yaml \
  --set-file spec=skillsmp.mcp.kdl \
  --set image.tag=<built-runtime-sha>
```

The chart lives here because the product **distributes as image + chart** (ward-mcp#6). It stays generic and spec-opaque: it takes the `.mcp.kdl` as an opaque blob and never parses it, so the [interior-only scope](DESIGN.md) of the spec is preserved - the spec still never reaches down into a chart, and the chart never reaches up into the spec. [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) consumes the chart per-MCP with one values file (deploy#61 skillsmp, deploy#46 forgejo) and owns the rollout.

## What it templates

| template | what it renders |
|---|---|
| `configmap-spec.yaml` | the `.mcp.kdl` from `--set-file spec=...`, mounted at `/spec/<name>.mcp.kdl` - the only per-MCP artifact |
| `deployment.yaml` | the one generic runtime, `ward-mcp serve /spec/<name>.mcp.kdl --http :8080`, the token(s) injected as env |
| `service.yaml` | ClusterIP, or NodePort when `service.nodePort` is set (the tailnet local-harness path) |
| `externalsecret.yaml` | an ExternalSecret pulling each SSM-path `secret` entry into the env var the guardfile's `value env <VAR>` names |
| `route.yaml` | the deploy#30 Authelia-JWT public overlay (Ingress + ForwardAuth + oauth2-proxy + RFC 9728 metadata sidecar), rendered only when `route.public` is true |

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
- **`service.nodePort`** - set it to bind a NodePort for the tailnet-direct local-harness path (deploy#39); unset leaves the Service ClusterIP-only. Pick a non-colliding value per MCP (the fleet convention is `30000` + the last three digits of a chosen port).

### Route (the public Authelia gate)

- **`route.enabled`** - master switch for any public routing.
- **`route.public`** - `true` mounts the full Authelia-JWT public overlay; `false` is tailnet-only (no public Ingress; reach the pod via the NodePort). A write-capable surface stays tailnet-only; a read surface can go public-gated - the Authelia gate is the security boundary, not the read/write split.
- **`route.host`** - the public hostname (required when `public`).
- **`route.clusterIssuer`** - cert-manager ClusterIssuer for the TLS cert (`letsencrypt-production`).
- **`route.externalDnsTarget`** - the home public IP for the ExternalDNS record, stamped by the deploy at rollout (empty here so nothing host-identifying lands in this repo).
- **`route.auth.clientId`** - the statically-registered Authelia OIDC client id (required when `public`).
- **`route.auth.issuerUrl`** - the Authelia issuer (`https://auth.coilysiren.me`).
- **`route.auth.extraAudiences`** - added to the default `https://<host>/mcp` and `https://<host>` audiences.
- **`route.auth.clientSecretRef`** - SSM path for the oauth2-proxy client-secret; defaults to the shared `/authelia/oidc-claude-client-secret` path. There is no `cookieSecretRef` - the chart asks External Secrets Operator to generate the cookie-secret in-cluster into a `<release>-oauth2-proxy-cookie` Secret, so no cookie-secret SSM param exists and Helm never needs `lookup`.

### Pod

- **`replicaCount`, `resources`, `nodeSelector`, `tolerations`, `affinity`, `podSecurityContext`, `securityContext`, `readinessProbe`, `livenessProbe`, `extraEnv`** - the usual pod knobs. `nodeSelector` pins to `kai-server` by default (the single-node homelab); clear it for a generic render.

## Operator prerequisites (per public MCP, off this chart)

The overlay assumes the fleet's shared Authelia gate is already stood up (see [deploy's authelia service](https://forgejo.coilysiren.me/coilyco-bridge/deploy/src/branch/main/services/authelia)). Per public MCP:

- **SSM params** - the app token the guardfile names, plus the oauth2-proxy `client-secret` for the gate (it reuses the shared Authelia hash by default). The cookie-secret is no longer a manual param - External Secrets Operator generates it in-cluster and preserves it across upgrades.
- **Authelia client** - register the `route.auth.clientId` OIDC client in deploy's authelia service and re-roll it.
- **External Secrets Operator** - the cluster needs ESO installed with generator support, because the public overlay now asks ESO to mint the oauth2-proxy cookie-secret in-cluster.
- **claude.ai connector** - register `https://<host>/mcp` as a custom connector with the client id + plaintext secret. The irreducible per-service human step.

## Verify

```sh
kubectl -n <ns> get pods -o wide
curl -s  https://<host>/.well-known/oauth-protected-resource   # 200, unauthenticated
curl -sI https://<host>/mcp                                     # 401 without a Bearer JWT
```

Tailnet-only releases have no public host; reach them at `http://kai-server:<nodePort>/mcp` over the tailnet, or `kubectl port-forward` the Service.
