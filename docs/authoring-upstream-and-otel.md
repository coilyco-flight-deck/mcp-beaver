# Upstream allowlists and telemetry

Authoring guidance for mcp-beaver guardfiles, split out of the README.

## Validating an upstream allowlist

`serve-upstream` has no spec file, so `lint-upstream` checks the allowlist
itself. It is offline unless you point it at an upstream, and it shares the
validation the serving path runs, so it cannot drift from what `serve-upstream`
accepts:

```sh
$ mcp-beaver lint-upstream --read-only heuristic --tool signoz_list_metrics --tool signoz_get_alert
signoz_get_alert
signoz_list_metrics
```

`--read-only heuristic` screens the names for mutation verbs. It is the offline
gate for review: a consumer repo declaring a read-only allowlist runs this in
CI, and a `create` / `update` / `delete` tool entering that list fails there,
with no live connection and no consumer-side pattern check.

It is a heuristic over names, so it can be wrong in both directions. When a
live upstream is reachable, `--read-only strict` supersedes it and asks the
upstream what is actually read-only:

```sh
$ mcp-beaver lint-upstream --upstream http://127.0.0.1:8000/mcp --read-only strict --tool rotate_key
mcp-beaver: upstream "http://127.0.0.1:8000/mcp" does not annotate these allowlisted tools readOnlyHint: rotate_key
```

A tool the upstream leaves unannotated counts as mutable: the MCP default for
`readOnlyHint` is false, so silence is not a promise. Because `--upstream`
builds the same proxy `serve-upstream` builds, it also fails a tool the
upstream does not expose at all. That connection makes it a rollout or smoke
step, not a CI one.

## Opt-in OpenTelemetry

mcp-beaver emits OpenTelemetry traces and metrics when standard `OTEL_*`
configuration explicitly selects an exporter or supplies an OTLP endpoint. An
unset telemetry environment is a no-network no-op. In particular, mcp-beaver
does not inherit autoexport's default OTLP exporter and does not dial a local
collector unless an operator opts in.

Common settings are:

* `OTEL_TRACES_EXPORTER=otlp|console|none`
* `OTEL_METRICS_EXPORTER=otlp|console|prometheus|none`
* `OTEL_EXPORTER_OTLP_ENDPOINT`, or the signal-specific
  `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` and
  `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`
* `OTEL_EXPORTER_OTLP_PROTOCOL=grpc|http/protobuf`, with signal-specific
  protocol overrides available through the standard variables
* `OTEL_SERVICE_NAME` and `OTEL_RESOURCE_ATTRIBUTES`
* `OTEL_SDK_DISABLED=true` to disable the SDK completely

The default `service.name` is the resolved MCP server name. MCP server and
upstream proxy client spans use the current MCP semantic conventions and
propagate W3C trace context plus baggage through `params._meta`. The matching
operation-duration histograms use the recommended explicit buckets. Direct
`POST /api/{tool-name}` requests receive standard HTTP telemetry plus one
logical `execute_tool <tool-name>` span. `/healthz` is excluded.

Instrumentation never records tool arguments, results, request or response
bodies, authorization headers, tokens, Guardfile contents, spec paths, or
upstream URLs. Export failures do not change tool results. Graceful process
shutdown flushes active providers within a five-second bound.

The runtime is a **thin shell** over umbra's [`http/opcore`](https://forgejo.coilysiren.me/coilyco-flight-deck/umbra) engine: `opcore.ParseInline` parses the inline spec, including typed body blocks, exact nested-string body mapping, typed and bounded query fields, repeated query arrays, mutually-exclusive query groups, and safe local aliases for upstream parameter names. Each grant projects to one MCP tool and one HTTP endpoint, and every call fires through the same self-guarding `opcore.Operation.Execute` (metachar gate, `restrict`, outbound auth). mcp-beaver retains MCP JSON types when routing arguments, so opcore validates the declared contract before an upstream call and serializes arrays as repeated keys in caller order. Successful calls return both the original text content and a structured `{result: ...}` value that conforms to the advertised output schema. mcp-beaver adds only the grant→tool projection and transport layers.

## See also

- [authoring-siblings.md](authoring-siblings.md) - guardfile siblings.
- [authoring-grants.md](authoring-grants.md) - grants and request bodies.
- [FEATURES.md](FEATURES.md) - what ships today.
