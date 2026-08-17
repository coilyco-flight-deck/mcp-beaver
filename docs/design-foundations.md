# Foundations

Tracking: [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept), [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).

## The one-line pitch

**A umbra Guardfile, no handwritten code, becomes a Docker image that serves a working MCP and matching HTTP tool API.**

`ward mcp build forgejo-issues.mcp.kdl` bakes the guardfile plus one generic runtime into an OCI image whose ENTRYPOINT serves the Model Context Protocol over the official MCP Go SDK's streamable HTTP transport and automatically projects the same tools over HTTP. The `.mcp.kdl` is the whole contract: every `can` grant becomes one MCP tool and one `POST /api/{tool-name}` endpoint, with method, path template, and typed params authored inline. No per-server Go, no per-server Dockerfile, no per-server handler, and - the part that surprises people - **no per-tool input schema**, because the engine derives it from the inline op definition.

## Scope: the spec configures the image interior, and stops there

The `.mcp.kdl` spec configures **only the interior of the Docker image**: which upstream API, how it authenticates, and which grants become which tools - the behavior of the server process running inside the container. mcp-beaver's job ends at a runnable image that serves MCP over `/mcp` and projects the same tools over `/api/{tool-name}`.

Everything about **exposing** that image is out of scope for the spec and for mcp-beaver:

* the k3s Deployment, Service, and port mapping,
* the ingress or tailnet route and public-vs-tailnet decision,
* injecting `FORGEJO_TOKEN` as a mounted Secret,

are templated by the generic **chart** mcp-beaver ships (see [Distribution](#distribution-image--chart) below), which [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) consumes per-MCP with one values file and rolls out. This keeps the dependency direction clean, the same rule deploy already follows: **the image stays unaware of how it is deployed.** The spec never reaches down into a chart, and the chart never reaches up into the spec - the chart is generic and treats the `.mcp.kdl` as opaque values it mounts, never parses.

## Why network HTTP and never stdio

The image serves MCP over the SDK-backed streamable HTTP transport and each tool over an ordinary JSON HTTP endpoint because the servers are meant to run always-on on kai-server's k3s cluster and be reached **by URL over the network** - like deploy's existing `steam-mcp`, `reddit-mcp`, `node-stats-mcp`, `repo-recall`. A remote pod cannot be driven over stdio, which co-locates server with client. So the interior always binds one HTTP listener, never a stdio loop. There is no transport fork to decide. (The *listener* is interior; the *route to it* is deploy's.)

## Not built on ward. Not built on cli-mcp. Built on umbra.

mcp-beaver shares **no code with [ward](https://forgejo.coilysiren.me/coilyco-flight-deck/ward)**, whose name it used to carry as `ward-mcp`. The `ward` in `wrap ward mcp <name>` is umbra's inline grammar, not a dependency here, and it moves when umbra moves. It is a driver over [umbra](https://forgejo.coilysiren.me/coilyco-flight-deck/umbra)'s three-layer spec engine. [cli-mcp](https://github.com/coilysiren/cli-mcp) is read as a **code reference only** - mcp-beaver uses the official MCP Go SDK for transport/session plumbing and does not depend on cli-mcp.

umbra already turns a Guardfile into a guarded surface, in three layers ([specverb.md](https://forgejo.coilysiren.me/coilyco-flight-deck/umbra/src/branch/main/docs/specverb.md)):

* **L0 - upstream spec.** In umbra generally, the vendor's OpenAPI/Swagger truth, embedded and pruned to the granted operations. In mcp-beaver's inline mode, the `.mcp.kdl` itself is the authored source and no fetched OpenAPI is a build input.
* **L1 - policy IR.** The compiled operation set. verb+resource resolves to an operation by convention; `op` overrides.
* **L2 - Guardfile.** The human authoring layer, pure KDL data, parsed never evaluated.

The engine carries zero upstream knowledge, so **one engine drives every spec, no code changes.** Its `specverb.Build` mounts each grant as a generic guarded action: path params become positionals, query and body fields become typed flags, all validated before the wire call. umbra's own `kdl-specs` driver renders that into a no-code **CLI**. mcp-beaver renders the same engine's inline operation set into **MCP tools and matching HTTP endpoints** instead.

## What mcp-beaver adds on top of umbra

* umbra provides inline operation parsing, guards, request assembly, and authentication.
* mcp-beaver provides grant-to-tool projection, derived MCP metadata, matching HTTP endpoint projection, the SDK-backed streamable HTTP transport, the runtime image, and the spec-opaque auth-neutral chart.
* deploy composes the runtime contract with ingress, authentication, TLS, DNS, per-MCP values, and rollout.

A tool call arrives over the SDK-backed MCP session or `POST /api/{tool-name}`, runs through the same registered handler and umbra guard (restrict gate, argv metachar gate on URL-bound inputs), resolves to an HTTP request, is signed with the injected upstream secret, and fires upstream. Both projections return the MCP `CallToolResult` JSON shape.

## Automatic dual projection

Every projected tool is always available through two protocol faces:

* MCP clients use `tools/call` over the SDK-backed `/mcp` session.
* Ordinary HTTP clients send the same JSON argument object to
  `POST /api/{tool-name}` without creating an MCP session.

The HTTP route is not a second authorization surface. It looks up the same
deny-by-absence tool registry and calls the exact handler registered with the
MCP SDK. Inline opcore tools, allowlisted upstream MCP tools, and exact-parameter
SSM tools all use the same projection. There is no flag, chart value, or
per-tool handler to drift.

mcp-beaver does not authenticate inbound callers or trust identity-shaped request
fields. Deploy owns the identity proxy, workload identity, network boundary,
TLS, and ingress policy. Guardfile `auth` is the credential mcp-beaver uses to
call its upstream. It is not caller authentication.

The MCP protocol surface keeps `initialize`, `ping`, `tools/list`, and
`tools/call` today. Any future resource or prompt support must stay on the
generic MCP names (`resources/list`, `resources/read`, `prompts/list`,
`prompts/get`). Ward-specific admin, lifecycle, reload, or control verbs do not
belong in the MCP surface.

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

These endpoints sit behind the same deployment-owned authentication assumptions
as `/mcp` and `/api/`. mcp-beaver itself performs no inbound authentication.

## See also

- [design-tool-metadata.md](design-tool-metadata.md)
- [design-pipeline-and-distribution.md](design-pipeline-and-distribution.md)
- [design-spec-dialect.md](design-spec-dialect.md)
- [design-milestones.md](design-milestones.md)
- [DESIGN.md](DESIGN.md) - the index.
