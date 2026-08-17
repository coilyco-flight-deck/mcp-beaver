# Conformance and upstream proxying

The living inventory of what mcp-beaver ships today. Completes the
README / AGENTS / docs/FEATURES trifecta. mcp-beaver turns a umbra Guardfile
into a guarded MCP server with an automatic matching HTTP tool API, distributed
as a runtime image plus a generic Helm chart.

## MCP 2026-07-28 conformance

* **Stateless transport** - the streamable HTTP handler is stateless, which the
  current revision requires; a session-backed handler rejects a 2026-07-28
  client outright. Pre-2026 clients still negotiate their own version.
* **Cacheable lists** - list results carry `ttlMs` and `cacheScope`. A
  spec-driven surface changes only when the pod restarts and gets the longer
  hint; a proxied surface mirrors a drifting upstream and gets the shorter one.
* **Deprecations** - the runtime no longer advertises the deprecated `logging`
  capability. Observability is OpenTelemetry, which it already emits.

## `mcp-beaver lint-upstream --tool <name>...`

The validation surface for a `serve-upstream` allowlist, the counterpart to
what `lint` gives a guardfile. An allowlist has no spec file to build, so the
checks are the allowlist's own shape plus, optionally, upstream truth.

* **Shared authority** - shape validation calls the same `ValidateAllowlist`
  the serving path calls, so the check cannot drift from what `serve-upstream`
  will accept. Empty entries, duplicates, and an empty list all fail.
* **Offline by default** - no network unless `--upstream` is passed, so it runs
  in CI and a sealed clone. A clean allowlist exits 0 and prints the names
  sorted, one per line, the list a consumer diffs against a reviewed
  expectation.
* **`--read-only heuristic`** - screens tool names offline for mutation verbs
  and fails naming any suspect. A naming heuristic, not upstream truth, but it
  lives in the owning loader instead of being restated per consumer.
* **`--read-only strict`** - requires `--upstream`, connects, and fails any
  allowlisted tool the upstream does not annotate `readOnlyHint: true`. This is
  authoritative and supersedes the heuristic, so it belongs in a rollout or
  smoke path rather than offline CI. An unannotated tool counts as mutable: the
  MCP default for the hint is false.
* **Existence check** - passing `--upstream` builds the same proxy
  `serve-upstream` builds, so a tool absent upstream fails there too.

## `mcp-beaver serve-upstream --upstream <mcp-url> --tool <name>...`

The guarded passthrough backend. It connects to a private streamable-HTTP MCP
upstream, snapshots the allowlisted upstream tool contracts, and exposes only
that subset on the outward MCP and automatic HTTP tool surfaces.

* **Upstream tool projection** - each allowlisted upstream tool becomes one
  outward MCP tool and one matching HTTP endpoint, preserving the upstream
  schema and tool metadata where possible.
* **Fail closed** - unknown upstream tools and schema drift return MCP tool
  errors instead of silently widening or mutating the surface.
* **Proxy calls** - allowed `tools/call` requests are forwarded to the upstream
  MCP session after the guard checks. One long-lived session serves every call,
  including the drift check, because a second session is what real Node MCP
  upstreams reject.
* **Session survival** - the upstream bound is time-to-first-byte, never a
  whole-exchange `Client.Timeout`: a streamable-HTTP response is a body that
  stays open, so a whole-exchange bound killed any long tool call and took the
  session with it. A session the upstream has forgotten is replaced on the next
  call rather than being fatal for the life of the pod. The failing call is not
  replayed - it may already have reached the upstream - and the reconnect never
  re-snapshots the baseline, so drift still fails closed. See
  [DESIGN.md](DESIGN.md).
* **Bounded startup retry** - optional `--connect-timeout` retries the initial
  MCP connection while a co-located upstream starts. Zero retains fail-fast
  behavior for direct CLI use.

## `mcp-beaver serve-ssm <spec.mcp.kdl> --http :addr`

The AWS SDK-backed exact-parameter reader. Its KDL policy names one parameter
and grants exactly `get_parameter(name)` plus `get_forgejo_read_token()`.
The general tool rejects every other name before AWS receives a request, while
IAM independently restricts the workload principal to the same parameter ARN.

## Transports

The runtime exposes the SDK-backed streamable HTTP transport, an automatic HTTP
tool API, and a liveness probe:

* **Streamable HTTP** (`/mcp`, SDK-backed) - `initialize`, `tools/list`, and
  `tools/call` ride the MCP Go SDK's session lifecycle and session IDs.
* **Automatic HTTP tool API** (`POST /api/{tool-name}`) - every projected MCP
  tool receives one matching endpoint without a flag or chart value. The JSON
  request body is the tool argument object. A successful response is the MCP
  `CallToolResult` JSON shape. Both surfaces call the same handler, so opcore
  guards, upstream schema-drift checks, and exact-parameter SDK policy cannot
  diverge. Requests require `application/json` and are bounded to 1 MiB.
  Unknown tools, invalid inputs, oversized bodies, tool errors, and handler
  errors return non-2xx JSON responses.
* **Health** - `GET /healthz` for a pod liveness probe.

mcp-beaver does not authenticate inbound MCP or HTTP callers. The consuming
deployment owns identity, authentication, TLS, ingress, and network exposure.
Guardfile authentication is outbound authentication from mcp-beaver to the
configured upstream. Caller-supplied identity-shaped tool arguments are data,
not trusted identity.

## See also

- [features-serve-and-lint.md](features-serve-and-lint.md)
- [features-guardfile-siblings.md](features-guardfile-siblings.md)
- [features-observability.md](features-observability.md)
- [features-bounds-and-packaging.md](features-bounds-and-packaging.md)
- [FEATURES.md](FEATURES.md) - the index.
