# Logs, telemetry, and transports

The living inventory of what mcp-beaver ships today. Completes the
README / AGENTS / docs/FEATURES trifecta. mcp-beaver turns a umbra Guardfile
into a guarded MCP server with an automatic matching HTTP tool API, distributed
as a runtime image plus a generic Helm chart.

## Structured logs

Every generated server writes JSON to stderr, one object per line, through
`log/slog`. The node collector reads `/var/log/pods/*` and the ingest pipeline
promotes JSON bodies and maps `level` onto OTel severity, so this needs no new
parser downstream.

Generated servers previously emitted exactly one line each - the startup banner
- and nothing per call. Twelve hours across twenty-two pods produced **one** log
line fleet-wide while four of those servers were failing every call, and each
failure had to be inferred from client spans because the server that knew what
went wrong said nothing.

* **Startup** - mode, server name, spec path, bound address, tool count, and
  request timeout, as fields rather than a sentence. An operator reading a
  fleet of these needs the bound config queryable, not greppable.
* **Every tool call** - `tool`, `outcome`, `duration_ms`, plus `trace_id` and
  `span_id` when a span is active, so a log line joins to its trace. Applied at
  registration, so grants, the info tool, withheld stubs, the SSM readers and
  the upstream proxy are all covered by construction.
* **Refusals** - `outcome=tool_error` at WARN with the reason, which is the gap
  that mattered: a server rejecting a call in 16 milliseconds is a server-side
  decision that had no server-side record anywhere. A handler failure is
  `outcome=handler_error` at ERROR.
* **Redaction** - a reason keeps each URL's scheme, host and path and drops its
  query, marked `?<redacted>` rather than silently removed. That line is not
  arbitrary: `pin` writes query parameters and only query parameters, and
  `auth` writes a header, so dropping the query removes exactly the surfaces a
  credential reaches while a 404 stays attributable. Reasons are bounded at 512
  characters, because an upstream that refuses with an HTML error page would
  otherwise put the page in the log line.
* **Level** - `MCP_BEAVER_LOG_LEVEL` (`debug` | `info` | `warn` | `error`),
  defaulting to info.

## Opt-in OpenTelemetry

The generic runtime provides application-level traces and metrics for every
spec-backed, SSM, and upstream-proxy server. Standard `OTEL_*` variables own
configuration. With no explicit exporter selector or OTLP endpoint, provider
initialization stays a no-network no-op. `OTEL_SDK_DISABLED=true` and the
per-signal `none` selectors disable export explicitly. Invalid explicit
configuration fails startup, while asynchronous export failures never alter a
tool result.

* **MCP server** - one SERVER span and one
  `mcp.server.operation.duration` measurement per MCP request or notification.
  `tools/call` carries `gen_ai.operation.name=execute_tool` and the bounded tool
  name. Tool-error results set `error.type=tool_error` and ERROR status.
* **Upstream MCP client** - `serve-upstream` emits CLIENT spans and
  `mcp.client.operation.duration` across startup discovery, schema refresh,
  and actual tool calls. W3C trace context and baggage inject into upstream
  `params._meta`, preserving the inbound server-to-client chain.
* **Context boundary** - inbound `params._meta` supplies the remote MCP parent.
  The active streamable-HTTP transport span is linked when it is distinct.
* **Direct HTTP tools** - `POST /api/{tool-name}` receives standard HTTP server
  telemetry and exactly one logical `execute_tool <tool-name>` child span.
  MCP `tools/call` does not duplicate that logical span. `/healthz` is excluded.
* **Resource and lifecycle** - the resolved MCP server name is the default
  `service.name`. `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES` override
  or extend it. Graceful shutdown flushes trace and metric providers within a
  five-second bound.
* **Safe attributes** - signal attributes stay bounded to methods, projected
  tools, transport, runtime mode, and closed-set error classes. Arguments,
  results, bodies, authorization headers, tokens, Guardfile contents, spec
  paths, and upstream URLs are never captured. **Structured logs are the one
  narrow exception**, and only for a refusal reason: they keep the upstream
  host and path so a failure is attributable, and drop the query, which is
  where `pin` and caller input live. Startup logs name the spec path, which the
  operator supplied. Nothing else crosses.

## Server-side argument pins (upstream proxy)

`--pin <tool>.<arg>=<value>` on `serve-upstream` fixes one argument of one
proxied tool, applied by the wrapper rather than supplied by the caller.

`upstream.tools` allowlists tool **names**, which is the whole authority only
while the verb carries the scope. It fails whenever scope rides in an argument:
allowlisting one Bluesky read tool grants every account, because the account is
a parameter.

* **Non-overridable** - a caller naming the pinned argument with a different
  value is **refused**, not silently corrected. Quiet rewriting would let a
  model believe it read one scope while reading another, and a refusal is the
  outcome a prompt injection cannot widen. Supplying the pinned value passes.
* **Validated at startup** - a pin naming a tool outside the allowlist is a
  startup error. An operator believing a surface is scoped while nothing
  applies it is the failure worth refusing to boot over.
* **Exact values only.** Conjunctive pinning of free-form filter *expressions*
  is deliberately not implemented. AND-ing expressions needs the upstream's
  query language, and a wrong conjunction does not fail loudly - it silently
  widens, against a consumer whose output surface is public. Exact-value
  pinning either matches or refuses, with no such ambiguity.

  `from="query:<parameter>"` reads one query parameter out of a URL the
  resolved value holds, for the common case of a credential that arrives
  embedded in a URL - a private RSS or Atom feed, a signed link, a webhook
  endpoint. Without it the only way to pin one is to store a **second**,
  pre-split copy of the same credential, which is two things to rotate and one
  of them going stale silently. The extraction only ever narrows: it takes a
  component of an already-resolved server-side value, reaches no new source,
  and the pinned name stays out of the tool schema. A value that is not a URL,
  or that carries no such parameter, fails the call, and the error names the
  parameter rather than echoing the value.

## See also

- [features-serve-and-lint.md](features-serve-and-lint.md)
- [features-guardfile-siblings.md](features-guardfile-siblings.md)
- [features-conformance-and-upstream.md](features-conformance-and-upstream.md)
- [features-bounds-and-packaging.md](features-bounds-and-packaging.md)
- [FEATURES.md](FEATURES.md) - the index.
