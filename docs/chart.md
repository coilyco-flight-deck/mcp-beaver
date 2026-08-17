# The mcp-beaver Helm chart

The auth-neutral distribution vehicle mcp-beaver ships (mcp-beaver#6). **One
chart, one runtime image, many releases.** The chart can mount a `.mcp.kdl`
guardfile or wrap an existing streamable-HTTP MCP with an exact tool allowlist.
Adding an MCP runtime becomes a values file plus `helm upgrade`.

```sh
helm upgrade --install skillsmp mcp-beaver \
  -f skillsmp.values.yaml \
  --set-file spec=skillsmp.mcp.kdl \
  --set image.tag=<built-runtime-sha>
```

The chart lives here because the product **distributes as image + chart** (mcp-beaver#6). It stays generic and spec-opaque: it takes the `.mcp.kdl` as an opaque blob and never parses it, so the [interior-only scope](DESIGN.md) of the spec is preserved. The spec never reaches down into a chart, and the chart never reaches up into the spec. [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) composes this runtime contract with its fleet-specific exposure charts and owns rollout.

For an upstream MCP:

```sh
helm upgrade --install reader mcp-beaver \
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

## See also

- [chart-sidecars.md](chart-sidecars.md) - sidecars and the rest.
- [FEATURES.md](FEATURES.md) - what ships today.
