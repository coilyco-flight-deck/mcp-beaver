# Chart sidecars and extras

The rest of the generic Helm chart surface. Split from [chart.md](chart.md).

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
cost is the opposite failure: mcp-beaver binds immediately and answers
`tools/list` correctly while the sidecar is still loading, so the pod reports
Ready with its data plane down and takes traffic that errors.

The chart's default `readinessProbe` checks mcp-beaver's own port, which is right
for upstream mode and wrong for this one. The reference values gate readiness
on the sidecar's port instead, which a probe may target directly because the
containers share a namespace. Liveness stays on mcp-beaver: a wedged sidecar
should fail readiness and stop taking traffic, not restart the pod out from
under a healthy runtime.

Accepting the default and letting early requests fail is defensible for a
sidecar that starts fast. It is not the default here, because "fails briefly
after every rollout" is the kind of error that gets attributed to the wrong
component.

## Upgrading a release installed as `ward-mcp`

This chart was named `ward-mcp`. The name reaches `app.kubernetes.io/name`,
which is a selector label, and a Deployment's `spec.selector` is immutable, so
an upgrade that changes it is rejected by the API server rather than applied.

A release from before the rename keeps its names by setting
`nameOverride: ward-mcp`, which is what every deploy-owned values file already
does. Nothing about that is transitional debt to clean up later: the label is
the identity of a running object, and the only way to change it is to delete
the release and install it again.

A new install leaves `nameOverride` empty and gets the current name.

## Exposure belongs to the consumer

The product chart does not choose between public, private, or tailnet access. It also carries no fleet identity provider or ingress-controller assumptions. The MCP and automatic HTTP tool API share that boundary and receive no runtime-owned inbound authentication. CoilyCo deployments that need a public authenticated surface use deploy's `charts/ingress-public-authed` chart. Other consumers bring an equivalent composition for their environment.

## See also

- [chart-operations.md](chart-operations.md) - upgrading, exposure, and verification.
- [chart.md](chart.md) - what the chart templates.
