# ward-mcp design draft

Tracking: [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept), [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).

## The one-line pitch

**A cli-guard Guardfile, no handwritten code, becomes a Docker image that serves a working MCP over HTTP/SSE.**

`ward mcp build forgejo-issues.mcp.kdl` bakes the guardfile plus one generic runtime into an OCI image whose ENTRYPOINT serves the Model Context Protocol over SSE/streamable-HTTP. Every `can` grant becomes one MCP tool. No per-server Go, no per-server Dockerfile, no per-server MCP handler, and - the part that surprises people - **no per-tool input schema**, because the engine derives it.

## Scope: the spec configures the image interior, and stops there

The `.mcp.kdl` spec configures **only the interior of the Docker image**: which upstream API, how it authenticates, and which grants become which MCP tools - the behavior of the server process running inside the container. ward-mcp's job ends at a runnable image that serves MCP over HTTP/SSE.

Everything about **exposing** that image is out of scope for the spec and for ward-mcp:

* the k3s Deployment, Service, and port mapping,
* the ingress or tailnet route and public-vs-tailnet decision,
* injecting `FORGEJO_TOKEN` as a mounted Secret,

all live in [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) (deploy#40's domain), which consumes the published image as a `deploy/services/<name>/` entry. This keeps the dependency direction clean, the same rule deploy already follows: **the image stays unaware of how it is deployed.** The spec never reaches down into a chart, and the chart never reaches up into the spec.

## Why HTTP/SSE (interior) and never stdio

The image serves MCP over SSE/streamable-HTTP because the servers are meant to run always-on on kai-server's k3s cluster and be reached **by URL over the network** - like deploy's existing `steam-mcp`, `reddit-mcp`, `node-stats-mcp`, `repo-recall`. A remote pod cannot be driven over stdio, which co-locates server with client. So the interior always binds an HTTP listener, never a stdio loop. There is no transport fork to decide. (The *listener* is interior; the *route to it* is deploy's.)

## Not built on ward. Not built on cli-mcp. Built on cli-guard.

ward-mcp has **no relation to the ward codebase.** It is a driver over [cli-guard](https://github.com/coilysiren/cli-guard)'s three-layer spec engine. [cli-mcp](https://github.com/coilysiren/cli-mcp) is read as a **code reference only** - ward-mcp implements its own SSE/HTTP MCP server and does not depend on it.

cli-guard already turns a Guardfile into a guarded surface, in three layers ([specverb.md](https://github.com/coilysiren/cli-guard/blob/main/docs/specverb.md)):

* **L0 - upstream spec.** The vendor's OpenAPI/Swagger truth, embedded and pruned to the granted operations.
* **L1 - policy IR.** The compiled operation set. verb+resource resolves to an operation by convention; `op` overrides.
* **L2 - Guardfile.** The human authoring layer, pure KDL data, parsed never evaluated.

The engine carries zero upstream knowledge, so **one engine drives every spec, no code changes.** Its `specverb.Build` mounts each grant as a generic guarded action: path params become positionals, query and body fields become typed flags, all validated before the wire call. cli-guard's own `kdl-specs` driver renders that into a no-code **CLI**. ward-mcp renders the same engine's operation set into **MCP tools** instead, served over HTTP.

## What ward-mcp adds on top of cli-guard

| layer | who provides it |
|---|---|
| spec parse, op resolution, prune, guard, request assembly, auth | cli-guard (unchanged) |
| **grant -> MCP tool projection** (op descriptor -> JSON-schema + handler) | **ward-mcp** |
| **SSE / streamable-HTTP transport** (the MCP wire protocol, interior) | **ward-mcp** (cli-mcp as reference) |
| **image build** (guardfile + locks + runtime -> OCI image) | **ward-mcp** |
| k3s exposure (Service, route, Secret) | `deploy` (out of scope here) |

A tool call arrives over SSE, is validated against the derived schema, run through cli-guard's guard (restrict gate, argv metachar gate on URL-bound inputs), resolved to an HTTP request, signed with the injected secret, and fired upstream. The response renders back over the MCP channel.

## The safety story - the guardfile IS the tool surface

A ward-mcp server **cannot exceed its guardfile**, because the guardfile is the only source of the tools.

* Every `can` grant is one tool. There is no way to declare a tool the guardfile does not grant.
* `never delete issue` means no `delete_issue` tool is minted. A deny beats an allow.
* `restrict owner matches coily*` bounds every `{owner}` path param.
* The argv metachar gate runs on inputs that compose into the URL (path + query); body fields (issue titles, markdown) are exempt, as in cli-guard.

Handing a write-capable MCP to an agent (deploy#40's ask) is defensible: the blast radius is one small reviewable file, enforced at the transport, not trusted to the model.

## The pipeline (Guardfile -> image; deploy takes it from there)

```
forgejo-issues.mcp.kdl        ─┐
forgejo.swagger.v1.json.lock  ├─► ward mcp build ─► OCI image (serves MCP/HTTP)  │  consumed by deploy ─► k3s ─► SSE URL
specverb.lock                 ─┘        (CI on push)                             │  (out of ward-mcp scope)
                                        ward-mcp's boundary ───────────────────► │
```

1. **Author** the `*.mcp.kdl` (the only hand-edited artifact).
2. **Lock** (cli-guard's existing online step) fetches upstream Swagger, prunes it to the granted ops + their transitive `$ref` schemas, writes the two committed locks (`<spec>.lock.json`, `specverb.lock`). Reused verbatim.
3. **Build** bakes the guardfile + the two locks + the single static `ward-mcp` runtime into an image. The runtime is reused across every guardfile. The image ENTRYPOINT is `ward-mcp serve /spec/<name>.mcp.kdl --http :8080`: it parses the guardfile, registers one MCP tool per grant, and binds an HTTP/SSE listener on a default port.

That is the whole of ward-mcp. **Consuming the image** - deploying it to k3s, mapping the port, routing it (tailnet-only for write-capable surfaces), mounting the token Secret - is deploy#40's work, described there, not here.

### "Automatic" = a CI consequence of a landed guardfile

A push touching a `.mcp.kdl` triggers the image build in CI, the way kai-server rebuilds a service image on push today (kai-server is the CI - no external pipeline). Landing the guardfile **is** publishing the image.

## The spec dialect

The body **is** the cli-guard Guardfile - see [`examples/forgejo-issues.mcp.kdl`](../examples/forgejo-issues.mcp.kdl). The whole surface is the `can` / `never` grants; input schemas are derived, not authored. The `.mcp.kdl` suffix marks the ward-mcp target and keeps the file out of cli-guard's CLI-discovery glob.

### Resolved

* **Filename** - `.mcp.kdl`. Exclusively an HTTP MCP image, not also a CLI, so the file names its target.
* **Transport** - SSE / streamable-HTTP only, bound inside the image. No stdio.
* **cli-mcp** - code reference only, not a dependency.
* **Serve knobs** - the image binds a default HTTP port; the spec stays **pure policy** and carries no `serve` block. Port mapping and path routing are k3s concerns owned by `deploy`, consistent with the interior-only scope.

### Still open

* **Tool naming** - `create_issue` (verb_resource) vs `forgejo_create_issue` (wrap-group-prefixed) when an agent mounts several ward-mcp servers at once. A cross-server concern, so plausibly the client's problem, not the spec's.

## First milestone (matches deploy#40)

Narrowest end-to-end slice: the `forgejo-issues` guardfile, one grant (`can create issue`), built to an image that serves `create_issue` over SSE. deploy#40 then stands that image up as a tailnet-only k3s service an agent mounts by URL. Everything else (comment, list, close, other upstreams) is additive once the spine works, and needs no new grammar.
