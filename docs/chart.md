# The mcp-beaver Helm chart

The auth-neutral distribution vehicle. One chart, one runtime image, many
releases. It mounts a `.mcp.kdl` guardfile or wraps an existing
streamable-HTTP MCP with an exact tool allowlist, so adding an MCP runtime
becomes a values file plus `helm upgrade`.

```sh
helm upgrade --install skillsmp mcp-beaver \
  -f skillsmp.values.yaml \
  --set-file spec=skillsmp.mcp.kdl \
  --set image.tag=<built-runtime-sha>
```

The chart stays generic and spec-opaque: it takes the `.mcp.kdl` as an opaque
blob and never parses it, so the interior-only scope of the spec is preserved.
The spec never reaches down into a chart, and the chart never reaches up into
the spec. `deploy` composes this runtime contract with its fleet-specific
exposure charts and owns rollout.

## What it templates

- `configmap-spec.yaml` renders wherever a `spec` is mounted, putting the
  `.mcp.kdl` at `/spec/<name>.mcp.kdl` beside any `widgets` an `app`-bearing
  guardfile reads. The volume enumerates what it projects, so a ConfigMap key
  added without a matching item stays unmounted. See [apps.md](apps.md).
- `deployment.yaml` renders the runtime in spec or upstream mode, injects
  application tokens when requested, and appends optional co-located
  containers.
- `service.yaml` renders a ClusterIP, or a NodePort when `service.nodePort` is
  set.
- `externalsecret.yaml` pulls each SSM-path `secret` entry into the environment
  variable named by the guardfile.

## Upstream mode, as values or as a guardfile

A passthrough proxy reaches the pod two ways, and `runtime.mode: upstream`
carries both. Set the `upstream` block and the chart renders `serve-upstream`
with `--upstream`, `--tool`, `--upstream-header`, and `--oauth2-client`. Set
`spec` to an `mcp-upstream` guardfile instead and the chart mounts it exactly as
spec mode mounts one, then runs `serve-upstream /spec/<specName>.mcp.kdl`. The
file names the endpoint, the allowlist, the credential, and the server, so the
chart refuses `upstream.url`, `.name`, `.tools`, `.headers`, and
`.oauth2Clients` beside it, the way `serve-upstream` refuses a file beside
`--upstream`. `upstream.connectTimeout` is the one key that still applies.

```sh
helm upgrade --install tandem-docs mcp-beaver \
  -f registry-upstream.values.yaml \
  --set-file spec=registry-upstream.mcp.kdl \
  --set image.tag=<built-runtime-sha>
```

`just helm-template-upstream-spec` renders that shape from the committed pair
in `examples/`. See [upstream.md](upstream.md).

It renders no Ingress, authentication proxy, identity-provider integration,
certificate, DNS record, or RFC 9728 metadata endpoint. The consuming
deployment owns those.

See also: [chart-values.md](chart-values.md), [transports.md](transports.md).
