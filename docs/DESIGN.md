# ward-mcp design draft

Tracking: [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept), [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).

## The one-line pitch

**A cli-guard Guardfile, no handwritten code, becomes a Docker image running an HTTP/SSE MCP server on k3s.**

`ward mcp build forgejo-issues.mcp.kdl` bakes the guardfile plus one generic runtime into an OCI image whose ENTRYPOINT serves the Model Context Protocol over SSE/streamable-HTTP. Every `can` grant becomes one MCP tool. No per-server Go, no per-server Dockerfile, no per-server MCP handler, and - the part that surprises people - **no per-tool input schema**, because the engine derives it.

## Why HTTP/SSE only, never stdio

The reason ward-mcp exists is to run these servers **on kai-server's k3s cluster**, deployed the way [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy)'s existing MCP services are (`steam-mcp`, `reddit-mcp`, `node-stats-mcp`, `repo-recall`). A k3s pod is a remote, always-on process. A client reaches it **by URL over the network**, not by spawning a local subprocess. So the stdio transport - which co-locates the server with its client - is useless here by construction. ward-mcp is **exclusively** an SSE/streamable-HTTP server. There is no transport fork to decide.

## Not built on ward. Not built on cli-mcp. Built on cli-guard.

ward-mcp has **no relation to the ward codebase.** It is a driver over [cli-guard](https://github.com/coilysiren/cli-guard)'s three-layer spec engine. [cli-mcp](https://github.com/coilysiren/cli-mcp) is read as a **code reference only** - ward-mcp implements its own SSE/HTTP MCP server and does not depend on it.

cli-guard already turns a Guardfile into a guarded surface, in three layers ([specverb.md](https://github.com/coilysiren/cli-guard/blob/main/docs/specverb.md)):

* **L0 - upstream spec.** The vendor's OpenAPI/Swagger truth, embedded and pruned to the granted operations.
* **L1 - policy IR.** The compiled operation set. verb+resource resolves to an operation by convention; `op` overrides.
* **L2 - Guardfile.** The human authoring layer, pure KDL data, parsed never evaluated.

The engine carries zero upstream knowledge, so **one engine drives every spec, no code changes.** Its `specverb.Build` mounts each grant as a generic guarded action: path params become positionals, query and body fields become typed flags, all validated before the wire call. cli-guard's own `kdl-specs` driver renders that into a no-code **CLI**. ward-mcp renders the same engine's operation set into **MCP tools** instead, and serves them over HTTP.

## What ward-mcp adds on top of cli-guard

| layer | who provides it |
|---|---|
| spec parse, op resolution, prune, guard, request assembly, auth | cli-guard (unchanged) |
| **grant -> MCP tool projection** (op descriptor -> JSON-schema + handler) | **ward-mcp** |
| **SSE / streamable-HTTP transport** (the MCP wire protocol) | **ward-mcp** (cli-mcp as reference) |
| **image build + k3s deploy surface** | **ward-mcp** + `deploy` conventions |

A tool call arrives over SSE, is validated against the derived schema, run through cli-guard's guard (restrict gate, argv metachar gate on URL-bound inputs), resolved to an HTTP request, signed with the run-time-injected secret, and fired upstream. The response renders back over the MCP channel.

## The safety story - the guardfile IS the tool surface

A ward-mcp server **cannot exceed its guardfile**, because the guardfile is the only source of the tools.

* Every `can` grant is one tool. There is no way to declare a tool the guardfile does not grant.
* `never delete issue` means no `delete_issue` tool is minted. A deny beats an allow.
* `restrict owner matches coily*` bounds every `{owner}` path param.
* The argv metachar gate runs on inputs that compose into the URL (path + query); body fields (issue titles, markdown) are exempt, as in cli-guard.

Handing a write-capable MCP to an agent (deploy#40's ask) is defensible: the blast radius is one small reviewable file, enforced at the transport, not trusted to the model.

## The pipeline (Guardfile -> image -> k3s service)

```
forgejo-issues.mcp.kdl        ─┐
forgejo.swagger.v1.json.lock  ├─► ward mcp build ─► OCI image ─► k3s Deployment ─► SSE endpoint
specverb.lock                 ─┘        (CI on push)              (deploy/services/*)   an agent mounts by URL
```

1. **Author** the `*.mcp.kdl` (the only hand-edited artifact).
2. **Lock** (cli-guard's existing online step) fetches upstream Swagger, prunes it to the granted ops + their transitive `$ref` schemas, writes the two committed locks (`<spec>.lock.json`, `specverb.lock`). Reused verbatim.
3. **Build** bakes the guardfile + the two locks + the single static `ward-mcp` runtime into an image. The runtime is reused across every guardfile.
4. **Deploy** as a `deploy/services/<name>/` entry - `Dockerfile` (thin: base ward-mcp image + the spec), `chart/` exposing the SSE port, `deploy/` bundle wiring the tailnet proxy (write-capable MCPs stay tailnet-only, like `repo-recall`) or a public cert-manager route for read-only ones. The `FORGEJO_TOKEN` is a mounted k3s Secret, never in the image.
5. **Serve** - the pod runs `ward-mcp serve /spec/<name>.mcp.kdl --http :8080`, registers one MCP tool per grant, and an agent adds the pod's URL to its MCP config.

### "Automatic" = a CI consequence of a landed guardfile

A push touching a `.mcp.kdl` (or its deploy bundle) triggers the image build + rollout, the way kai-server rebuilds a service image on push today (kai-server is the CI - no external pipeline). Landing the guardfile **is** publishing the server.

## The spec dialect

The body **is** the cli-guard Guardfile - see [`examples/forgejo-issues.mcp.kdl`](../examples/forgejo-issues.mcp.kdl). The whole surface is the `can` / `never` grants; input schemas are derived, not authored. The `.mcp.kdl` suffix marks the ward-mcp target and keeps the file out of cli-guard's CLI-discovery glob.

### Resolved (were open in v1)

* **Filename** - `.mcp.kdl`. This is exclusively an HTTP MCP for k3s, not also a CLI, so the file names its target rather than pretending to be dual-use.
* **Transport** - SSE / streamable-HTTP only. No stdio. (Above.)
* **cli-mcp** - code reference only, not a dependency. ward-mcp ships its own HTTP MCP server.

### Still open

* **Tool naming** - `create_issue` (verb_resource) vs `forgejo_create_issue` (wrap-group-prefixed) when an agent mounts several ward-mcp servers at once.
* **Serve knobs in-spec vs flags** - does the `.mcp.kdl` carry an optional `serve { path "/mcp"; port 8080 }` block, or does the k3s chart own port/path entirely? Leaning chart-owns-it, spec stays pure policy.

## First milestone (matches deploy#40)

Narrowest end-to-end slice: the `forgejo-issues` guardfile, one grant (`can create issue`), built to an image, deployed as a tailnet-only `deploy/services/forgejo-issues-mcp/` k3s service, reachable at an SSE URL an agent mounts to open an issue on a `coily*` repo. Everything else (comment, list, close, other upstreams) is additive once the spine works, and needs no new grammar.
