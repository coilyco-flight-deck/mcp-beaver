# Pipeline and distribution

Tracking: [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept), [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).

## The pipeline (Guardfile -> image; deploy takes it from there)

```
inline `.mcp.kdl` + shared runtime ─► ward mcp build ─► OCI image (serves MCP/HTTP) ─► deploy ─► k3s ─► SSE URL
                                        (CI on push)                         (out of mcp-beaver scope)
```

1. **Author** the `*.mcp.kdl` inline. The method, path, and typed params are the reviewable surface.
2. **Build** bakes the guardfile plus the single static `mcp-beaver` runtime into an image. There is no vendored OpenAPI JSON, no `.lock.json`, no prune output, and no `op` pin in the build input.
3. The image ENTRYPOINT is `mcp-beaver serve /spec/<name>.mcp.kdl --http :8080`: it parses the guardfile, registers one MCP tool and one HTTP endpoint per grant, and binds an HTTP listener on a default port.

**Consuming the chart** - picking the host, the SSM token, and public-vs-tailnet for a given MCP, and rolling it out - is deploy's work (deploy#61, deploy#46), described there. mcp-beaver ships the generic chart the values feed; deploy owns the values.

## Distribution: image + chart

mcp-beaver distributes as **two generic artifacts** (mcp-beaver#6), not a per-server image alone:

1. the **runtime image** above - one static `mcp-beaver` binary, shared across every guardfile, and
2. an **auth-neutral Helm chart** (`chart/`) that templates the runtime layer:
   the Deployment, Service, application Secret wiring, and either a mounted
   spec or an exact upstream MCP allowlist.

Adding an MCP is then a **values file + `helm upgrade`**, with no per-service
image build. In spec mode the chart stays generic and **spec-opaque**: it
mounts the `.mcp.kdl` and never parses it. In upstream mode it passes the exact
tool allowlist to `serve-upstream` and may co-locate a loopback-only private
MCP. The full chart reference is in [chart.md](chart.md).

This does not move deployment policy into mcp-beaver. The consuming deployment chooses the host, application Secret source, network exposure, authentication, TLS, and DNS. CoilyCo's fleet-specific public gate lives in deploy's `charts/ingress-public-authed` chart. mcp-beaver ships the runtime mechanism, and deploy sets exposure policy.

### "Automatic" = a CI consequence of a landed guardfile

A push touching a `.mcp.kdl` triggers the image build in CI, the way kai-server rebuilds a service image on push today (kai-server is the CI - no external pipeline). Landing the guardfile **is** publishing the image.

## See also

- [design-foundations.md](design-foundations.md)
- [design-tool-metadata.md](design-tool-metadata.md)
- [design-spec-dialect.md](design-spec-dialect.md)
- [design-milestones.md](design-milestones.md)
- [DESIGN.md](DESIGN.md) - the index.
