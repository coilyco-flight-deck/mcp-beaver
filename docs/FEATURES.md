# ward-mcp features

The living inventory of what ward-mcp ships today. Completes the
README / AGENTS / docs/FEATURES trifecta.

## `ward-mcp serve <spec.mcp.kdl> --http :addr`

The generic runtime, and the only command. One static binary renders any
`.mcp.kdl` spec into a guarded MCP server over HTTP/SSE. No per-guardfile Go, no
per-server handler. It never binds stdio - these run as remote pods reached by
URL.

* **Spec parse** - `opcore.ParseInline` (cli-guard `http/opcore`, pinned
  `v0.80.0`) parses the frozen ward-mcp inline grammar: `wrap` header, `base-url`,
  `auth`, `restrict`, and each `can <verb> <resource> { path/query/body/set }`
  grant. Method is inferred from the verb, path params from the `{template}`.
* **Grant → MCP tool projection** - each `Descriptor` becomes one tool named
  `verb_resource`, its `inputSchema` derived (draft-07) from the grant's
  path/query/body, its description the grant sentence. `internal/mcpserver`.
* **Guarded execute** - a `tools/call` routes the MCP arguments onto
  `opcore.Args` (path/query → URL, body → JSON) and fires the self-guarding
  `opcore.Operation.Execute`: metachar gate, `restrict` allowlist, base-url, and
  env-token auth are the engine's, never re-implemented here. A denied or failed
  call returns as a tool result with `isError` set.
* **Deny-by-absence** - the served surface is exactly the `can` grants. An
  unwritten grant is an absent tool; that is the deletion guard.

## Transports

Two MCP HTTP transports, both interior to the image:

* **Streamable HTTP** (`/mcp`, 2025-03-26+) - one POST carries a JSON-RPC
  message; the reply is JSON or a single SSE frame per the client's `Accept`.
* **Legacy HTTP+SSE** (`/sse` + `/messages`, 2024-11-05) - `GET /sse` opens the
  stream and names the POST-back endpoint; `POST /messages` feeds a message whose
  reply is pushed back over the stream.
* **Health** - `GET /healthz` for a pod liveness probe.

Supported MCP methods: `initialize`, `notifications/initialized`, `ping`,
`tools/list`, `tools/call`.

## Image

A single [`Dockerfile`](../Dockerfile) builds the one generic runtime image
(distroless, nonroot). The spec is mounted or COPYed in and named on the command
line - the same binary drives every `.mcp.kdl`. Building the image is a CI
consequence of a landed commit; deploying it (k3s Service, route, token Secret)
is [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy)'s job
(deploy#40), not this repo's.

## Examples

* [`examples/forgejo-issues.mcp.kdl`](../examples/forgejo-issues.mcp.kdl) - the
  worked "hello world": five guarded issue tools scoped to `coilyco-*` / `kai`.
* [`examples/skillsmp.mcp.kdl`](../examples/skillsmp.mcp.kdl) - the first
  end-to-end target: two read tools over SSE against skillsmp.com.

## Not yet built

* **`action` composition** - one tool chaining several ops. Deferred until opcore
  exposes a composed chain (DESIGN.md `collect` follow-up).
* **Tool-name disambiguation** - `verb_resource` is lossy when a resource carries
  its own separator, and unprefixed across multiply-mounted servers. A naming
  follow-up, not a guard concern.
