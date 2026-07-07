# ward-mcp features

The living inventory completing the README / AGENTS / docs/FEATURES trifecta. ward-mcp turns a cli-guard Guardfile into a guarded MCP server, distributed as a runtime image plus a generic Helm chart.

## Shipped

- **The generic Helm chart** (`chart/`) - the distribution vehicle (ward-mcp#6). One chart, one runtime image, many releases: the `.mcp.kdl` rides in as values (a ConfigMap), the chart templates the k3s exposure (Deployment, Service, token Secret wiring, and the optional Authelia-JWT public route). Adding an MCP is a values file + `helm upgrade`. Spec-opaque, so the interior-only scope of the spec holds. See [chart.md](chart.md).
  - **public vs tailnet-only** - `route.public` toggles the full deploy#30 Authelia overlay (Ingress + ForwardAuth + oauth2-proxy + RFC 9728 metadata sidecar) against a tailnet-only NodePort. A write surface stays tailnet-only; a read surface can go public-gated.
  - **secret wiring** - `secret` maps each `value env <VAR>` the guardfile names to an SSM parameter path (chart mints an ExternalSecret) or an existing Secret ref.
- **Example specs + values** - `examples/forgejo-issues.mcp.kdl` (the worked "hello world"), plus reference deploy-side values for a public read (`skillsmp.values.yaml`) and a tailnet-only write (`forgejo-issues.values.yaml`).

## Drafted, not yet implemented

- **The runtime image** (`ward mcp build` -> an OCI image serving MCP over SSE/streamable-HTTP) - the interior the chart runs. Tracked at [inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164). The chart is built against its pinned contract (ENTRYPOINT `ward-mcp serve /spec/<name>.mcp.kdl --http :8080`, MCP at `/mcp`, token as env, spec read from the mounted path) and finalizes the mount path / port / probe once the runtime lands.

## See also

- [README.md](../README.md) - the pitch and the image + chart distribution.
- [DESIGN.md](DESIGN.md) - the Guardfile -> image pipeline, the interior-only scope, the safety model.
- [chart.md](chart.md) - the chart's templates, values reference, and runtime contract.
