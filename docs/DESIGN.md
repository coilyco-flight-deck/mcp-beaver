# ward-mcp design draft

Tracking: [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept), [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).

## The one-line pitch

**A cli-guard Guardfile, no handwritten code, becomes a Docker image that serves a working MCP over HTTP.**

`ward mcp build forgejo-issues.mcp.kdl` bakes the guardfile plus one generic runtime into an OCI image whose ENTRYPOINT serves the Model Context Protocol over the official MCP Go SDK's streamable HTTP transport. The `.mcp.kdl` is the whole contract: every `can` grant becomes one MCP tool, with method, path template, and typed params authored inline. No per-server Go, no per-server Dockerfile, no per-server MCP handler, and - the part that surprises people - **no per-tool input schema**, because the engine derives it from the inline op definition.

## Scope: the spec configures the image interior, and stops there

The `.mcp.kdl` spec configures **only the interior of the Docker image**: which upstream API, how it authenticates, and which grants become which MCP tools - the behavior of the server process running inside the container. ward-mcp's job ends at a runnable image that serves MCP over `/mcp`.

Everything about **exposing** that image is out of scope for the spec and for ward-mcp:

* the k3s Deployment, Service, and port mapping,
* the ingress or tailnet route and public-vs-tailnet decision,
* injecting `FORGEJO_TOKEN` as a mounted Secret,

are templated by the generic **chart** ward-mcp ships (see [Distribution](#distribution-image--chart) below), which [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) consumes per-MCP with one values file and rolls out. This keeps the dependency direction clean, the same rule deploy already follows: **the image stays unaware of how it is deployed.** The spec never reaches down into a chart, and the chart never reaches up into the spec - the chart is generic and treats the `.mcp.kdl` as opaque values it mounts, never parses.

## Why HTTP at /mcp and never stdio

The image serves MCP over the SDK-backed streamable HTTP transport because the servers are meant to run always-on on kai-server's k3s cluster and be reached **by URL over the network** - like deploy's existing `steam-mcp`, `reddit-mcp`, `node-stats-mcp`, `repo-recall`. A remote pod cannot be driven over stdio, which co-locates server with client. So the interior always binds an HTTP listener, never a stdio loop. There is no transport fork to decide. (The *listener* is interior; the *route to it* is deploy's.)

## Not built on ward. Not built on cli-mcp. Built on cli-guard.

ward-mcp has **no relation to the ward codebase.** It is a driver over [cli-guard](https://github.com/coilysiren/cli-guard)'s three-layer spec engine. [cli-mcp](https://github.com/coilysiren/cli-mcp) is read as a **code reference only** - ward-mcp uses the official MCP Go SDK for transport/session plumbing and does not depend on cli-mcp.

cli-guard already turns a Guardfile into a guarded surface, in three layers ([specverb.md](https://github.com/coilysiren/cli-guard/blob/main/docs/specverb.md)):

* **L0 - upstream spec.** In cli-guard generally, the vendor's OpenAPI/Swagger truth, embedded and pruned to the granted operations. In ward-mcp's inline mode, the `.mcp.kdl` itself is the authored source and no fetched OpenAPI is a build input.
* **L1 - policy IR.** The compiled operation set. verb+resource resolves to an operation by convention; `op` overrides.
* **L2 - Guardfile.** The human authoring layer, pure KDL data, parsed never evaluated.

The engine carries zero upstream knowledge, so **one engine drives every spec, no code changes.** Its `specverb.Build` mounts each grant as a generic guarded action: path params become positionals, query and body fields become typed flags, all validated before the wire call. cli-guard's own `kdl-specs` driver renders that into a no-code **CLI**. ward-mcp renders the same engine's inline operation set into **MCP tools** instead, served over HTTP.

## What ward-mcp adds on top of cli-guard

* cli-guard provides inline operation parsing, guards, request assembly, and authentication.
* ward-mcp provides grant-to-tool projection, the SDK-backed streamable HTTP transport, the runtime image, and the spec-opaque auth-neutral chart.
* deploy composes the runtime contract with ingress, authentication, TLS, DNS, per-MCP values, and rollout.

A tool call arrives over the SDK-backed MCP session, is validated against the derived schema, run through cli-guard's guard (restrict gate, argv metachar gate on URL-bound inputs), resolved to an HTTP request, signed with the injected secret, and fired upstream. The response renders back over the MCP channel.

## MCP boundary: generic surface only

ward-mcp intentionally exposes only the generic MCP protocol surface. It keeps
`initialize`, `ping`, `tools/list`, and `tools/call` today, and any future
resource or prompt support must stay on the generic MCP names (`resources/list`,
`resources/read`, `prompts/list`, `prompts/get`). Ward-specific admin,
lifecycle, reload, or control verbs do not belong in the MCP surface.

## Operator HTTP boundary: non-MCP control and inspection

The runtime also serves operator-only HTTP endpoints outside the MCP tool
surface:

* `GET /healthz` - unactionable liveness and readiness.
* `GET /admin/describe` - a safe runtime summary with the loaded guardfile,
  projected tool count, transport mode, upstream presence, and non-secret
  config facts.
* `POST /admin/reload` - explicit operator reload, currently restart-only. The
  runtime cannot safely swap its guarded state in place, so the endpoint says
  restart required instead of pretending otherwise.

These endpoints sit behind the same deployment/auth assumptions as the other
operator-only surfaces. The MCP surface stays model-facing only.

## The safety story - the guardfile IS the tool surface

A ward-mcp server **cannot exceed its guardfile**, because the guardfile is the only source of the tools.

* Every `can` grant is one tool. There is no way to declare a tool the guardfile does not grant.
* An unwritten `delete issue` grant means no `delete_issue` tool is minted. In the frozen `opcore.ParseInline` grammar this is **deny-by-absence**: the served surface is exactly the `can` grants, so leaving a grant out is the deletion guard.
* `restrict owner matches coilyco-*` bounds every `{owner}` path param.
* The argv metachar gate runs on inputs that compose into the URL (path + query); body fields (issue titles, markdown) are exempt, as in cli-guard.

Handing a write-capable MCP to an agent (deploy#40's ask) is defensible: the blast radius is one small reviewable file, enforced at the transport, not trusted to the model.

## The pipeline (Guardfile -> image; deploy takes it from there)

```
inline `.mcp.kdl` + shared runtime ─► ward mcp build ─► OCI image (serves MCP/HTTP) ─► deploy ─► k3s ─► SSE URL
                                        (CI on push)                         (out of ward-mcp scope)
```

1. **Author** the `*.mcp.kdl` inline. The method, path, and typed params are the reviewable surface.
2. **Build** bakes the guardfile plus the single static `ward-mcp` runtime into an image. There is no vendored OpenAPI JSON, no `.lock.json`, no prune output, and no `op` pin in the build input.
3. The image ENTRYPOINT is `ward-mcp serve /spec/<name>.mcp.kdl --http :8080`: it parses the guardfile, registers one MCP tool per grant, and binds an HTTP listener on a default port.

**Consuming the chart** - picking the host, the SSM token, and public-vs-tailnet for a given MCP, and rolling it out - is deploy's work (deploy#61, deploy#46), described there. ward-mcp ships the generic chart the values feed; deploy owns the values.

## Distribution: image + chart

ward-mcp distributes as **two generic artifacts** (ward-mcp#6), not a per-server image alone:

1. the **runtime image** above - one static `ward-mcp` binary, shared across every guardfile, and
2. an **auth-neutral Helm chart** (`chart/`) that templates the runtime layer: the spec as a mounted ConfigMap, the Deployment running the shared runtime, the Service, and application Secret wiring.

Adding an MCP is then a **values file + `helm upgrade`**, no per-guardfile image build and no per-service manifest fork. The chart stays generic and **spec-opaque**: it mounts the `.mcp.kdl` and never parses it, so the interior-only scope holds - the spec still knows nothing about deployment, and the chart knows nothing about any one spec's grants. The full chart reference is in [chart.md](chart.md).

This does not move deployment policy into ward-mcp. The consuming deployment chooses the host, application Secret source, network exposure, authentication, TLS, and DNS. CoilyCo's fleet-specific public gate lives in deploy's `charts/ingress-public-authed` chart. ward-mcp ships the runtime mechanism, and deploy sets exposure policy.

### "Automatic" = a CI consequence of a landed guardfile

A push touching a `.mcp.kdl` triggers the image build in CI, the way kai-server rebuilds a service image on push today (kai-server is the CI - no external pipeline). Landing the guardfile **is** publishing the image.

## The spec dialect

The body is the frozen ward-mcp inline grammar parsed by `opcore.ParseInline` - see [`examples/forgejo-issues.mcp.kdl`](../examples/forgejo-issues.mcp.kdl). The whole surface is the `can` grants (each with its `path`/`query`/`body`/`set`); input schemas are derived from those inline op definitions, not authored separately. The `.mcp.kdl` suffix marks the ward-mcp target and keeps the file out of cli-guard's CLI-discovery glob.

### Resolved

* **Filename** - `.mcp.kdl`. Exclusively an HTTP MCP image, not also a CLI, so the file names its target.
* **Transport** - SDK-backed streamable HTTP at `/mcp`, bound inside the image. No stdio.
* **cli-mcp** - code reference only, not a dependency.
* **Serve knobs** - the image binds a default HTTP port; the spec stays **pure policy** and carries no `serve` block. Port mapping and path routing are k3s concerns owned by `deploy`, consistent with the interior-only scope.

### Still open

* **Tool naming** - `create_issue` (verb_resource) vs `forgejo_create_issue` (wrap-group-prefixed) when an agent mounts several ward-mcp servers at once. A cross-server concern, so plausibly the client's problem, not the spec's. The shipped runtime projects the bare `verb_resource` (`opcore` Leaf `_` Group). A secondary wrinkle surfaced building the skillsmp example: a resource carrying its own separator (`ai-search`) makes the underscore-joined name lossy (`search_ai-skills` vs the vendor's `ai_search_skills`). Reconciling projection with an arbitrary vendor tool name is a naming follow-up, not a guard concern - the guarded surface is correct either way.
* **`action` composition** - one tool chaining several ops (e.g. `view issue` = issue + comments). opcore v0.80.0 exposes single-operation `Execute`/`Preview` but no composed chain, so ward-mcp defers this per DESIGN's `collect` follow-up and keeps every floor grant individually reachable. Additive once opcore exposes the chain; no new runtime spine.

### Implemented (ward-mcp#7)

The `ward-mcp serve <spec> --http :addr` runtime ships as a thin shell over cli-guard's `http/opcore` (pinned at `v0.80.0` in [`go.mod`](../go.mod)):

* `internal/mcpserver.New` runs `opcore.ParseInline`, wires the value providers (env/file/literal), and registers each `Descriptor` as one MCP tool on the official Go SDK server (name `verb_resource`, `inputSchema` from `Descriptor.InputSchema().JSONSchema()`, description from the grant sentence).
* A `tools/call` maps the MCP arguments onto `opcore.Args` by the schema's neutral Location hint (path/query→URL, body→JSON) and fires the self-guarding `opcore.Operation.Execute`; a guard/upstream failure returns as an MCP tool result with `isError` set, not a transport fault.
* The runtime serves `/mcp` through the SDK's streamable HTTP handler and `/healthz` for liveness. Never stdio.

## First milestone (matches deploy#40)

Narrowest end-to-end slice: the `forgejo-issues` guardfile, one grant (`can create issue`), built to an image that serves `create_issue` over the SDK-backed `/mcp` transport. deploy#40 then stands that image up as a tailnet-only k3s service an agent mounts by URL. Everything else (comment, list, close, other upstreams) is additive once the spine works, and needs no new grammar.
