# Spec dialect status

What the spec dialect has settled, what is still open, and what has landed. Split from the spec-dialect page.

### Resolved

* **Filename** - `.mcp.kdl`. Exclusively an HTTP MCP image, not also a CLI, so the file names its target.
* **Transport** - SDK-backed streamable HTTP at `/mcp` plus automatic `POST /api/{tool-name}`, bound on one listener inside the image. No stdio.
* **cli-mcp** - code reference only, not a dependency.
* **Serve knobs** - the image binds a default HTTP port; the spec stays **pure policy** and carries no `serve` block. Port mapping and path routing are k3s concerns owned by `deploy`, consistent with the interior-only scope.

### Still open

* **Tool naming** - `create_issue` (verb_resource) vs `forgejo_create_issue` (wrap-group-prefixed) when an agent mounts several mcp-beaver servers at once. A cross-server concern, so plausibly the client's problem, not the spec's. The shipped runtime projects the bare `verb_resource` (`opcore` Leaf `_` Group). A secondary wrinkle surfaced building the skillsmp example: a resource carrying its own separator (`ai-search`) makes the underscore-joined name lossy (`search_ai-skills` vs the vendor's `ai_search_skills`). Reconciling projection with an arbitrary vendor tool name is a naming follow-up, not a guard concern - the guarded surface is correct either way.
* **`action` composition** - one tool chaining several ops (e.g. `view issue` = issue + comments). opcore v0.80.0 exposes single-operation `Execute`/`Preview` but no composed chain, so mcp-beaver defers this per DESIGN's `collect` follow-up and keeps every floor grant individually reachable. Additive once opcore exposes the chain; no new runtime spine.

### Implemented (mcp-beaver#7)

The `mcp-beaver serve <spec> --http :addr` runtime ships as a thin shell over umbra's `http/opcore` (pinned at `v0.127.0` in [`go.mod`](../go.mod)):

* `internal/mcpserver.New` runs `opcore.ParseInline`, wires the value providers (env/file/literal), and registers each `Descriptor` as one MCP tool on the official Go SDK server plus one matching HTTP route. The name is `verb_resource`, `inputSchema` is the unchanged output of `Descriptor.InputSchema().JSONSchema()`, and client-facing metadata is derived as described above.
* A `tools/call` maps the MCP arguments onto `opcore.Args` by the schema's neutral Location hint (path/query→URL, body→JSON) without flattening JSON values, then fires the self-guarding `opcore.Operation.Execute`. A guard or upstream failure returns as an MCP tool result with `isError` set, not a transport fault.
* The runtime serves `/mcp` through the SDK's streamable HTTP handler, every registered tool through `POST /api/{tool-name}`, and `/healthz` for liveness. The MCP and HTTP calls share one handler. Never stdio.

## See also

- [design-spec-dialect.md](design-spec-dialect.md) - the dialect itself.
- [DESIGN.md](DESIGN.md) - the index.
