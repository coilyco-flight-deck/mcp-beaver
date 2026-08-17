# Image, chart, examples, and roadmap

Split from [features-bounds-and-packaging.md](features-bounds-and-packaging.md) to stay inside the band.

## The generic Helm chart (`chart/`)

The auth-neutral distribution vehicle (mcp-beaver#8). One chart, one runtime
image, many releases. `runtime.mode: spec` mounts a `.mcp.kdl` ConfigMap.
`runtime.mode: upstream` runs an exact passthrough allowlist, omits the
guardfile, and can co-locate a private MCP through `extraContainers`.
`extraContainers` is not gated on the mode: spec mode uses the same field to
wrap a co-located process that speaks plain HTTP JSON rather than MCP, with
`base-url` pointing at loopback. The chart
templates the Deployment, ClusterIP or optional NodePort Service, application
Secret wiring, and startup protection for sidecar-backed proxies. See
[chart.md](chart.md).

* **exposure-neutral** - the chart contains no ingress controller, identity
  provider, authentication proxy, certificate issuer, DNS provider, or
  protected-resource metadata assumptions. A consuming deployment brings its
  own exposure layer.
* **secret wiring** - `secret` maps each `value env <VAR>` the guardfile names to
  an SSM parameter path (chart mints an ExternalSecret) or an existing Secret ref.
  Upstream mode can disable injection into mcp-beaver while a co-located
  upstream consumes the generated Secret directly.

Deploying an MCP, including the host, exposure, authentication, TLS, DNS, and
rollout, is [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy)'s
job (deploy#40 / deploy#46 / deploy#61). Its shared
`charts/ingress-public-authed` chart owns the CoilyCo fleet gate. mcp-beaver ships
the auth-neutral runtime chart contract.

## Examples

* [`examples/forgejo-issues.mcp.kdl`](../examples/forgejo-issues.mcp.kdl) - the
  worked "hello world": five guarded issue tools scoped to `coilyco-*` / `kai`.
* [`examples/skillsmp.mcp.kdl`](../examples/skillsmp.mcp.kdl) - the first
  end-to-end target: two read tools over the SDK-backed transport against
  skillsmp.com.
* [`examples/*.values.yaml`](../examples/) - auth-neutral chart values: a
  ClusterIP read surface (`skillsmp`) and an optional NodePort write surface
  (`forgejo-issues`).
* [`examples/upstream.values.yaml`](../examples/upstream.values.yaml) - an
  allowlisted upstream proxy with a co-located MCP container.

## Not yet built

* **`action` composition** - one tool chaining several ops. Deferred until opcore
  exposes a composed chain (DESIGN.md `collect` follow-up).
* **Tool-name disambiguation** - `verb_resource` is lossy when a resource carries
  its own separator, and unprefixed across multiply-mounted servers. A naming
  follow-up, not a guard concern.

## See also

- [features-bounds-and-packaging.md](features-bounds-and-packaging.md) - the rest.
- [FEATURES.md](FEATURES.md) - the index.
