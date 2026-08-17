# mcp-beaver features

The living inventory of what mcp-beaver ships today. Completes the
README / AGENTS / docs/FEATURES trifecta. mcp-beaver turns a umbra Guardfile
into a guarded MCP server with an automatic matching HTTP tool API, distributed
as a runtime image plus a generic Helm chart.

## `mcp-beaver serve <spec.mcp.kdl> --http :addr`

The generic local runtime. One static binary renders any `.mcp.kdl` spec into
a guarded MCP server over the official MCP Go SDK's streamable HTTP transport
at `/mcp` and automatically exposes the identical tool arguments at
`POST /api/{tool-name}`. No per-guardfile Go, no per-server handler. It never
binds stdio - these run as remote pods reached by URL.

* **Spec parse** - `opcore.ParseInline` (umbra `http/opcore`, pinned
  `v0.131.0`) parses the mcp-beaver inline grammar: `wrap` header, `base-url`,
  `auth`, `restrict`, and each
  `can <verb> <resource> { path/query/body/set }` grant. Flat body declarations
  remain optional string shorthand. Body blocks preserve typed scalars, scalar
  arrays, nested objects, required fields, and unconstrained raw object or
  array subtrees. They can instead project required nested string inputs onto
  fresh top-level JSON keys without forwarding undeclared input. Query blocks
  preserve string, boolean, integer, number, and
  scalar-array types plus numeric bounds, array length bounds, required fields,
  mutually-exclusive groups, and safe local aliases for upstream parameter
  names. The `.mcp.kdl` is the whole contract. Method is inferred from the verb,
  path params from the `{template}`.
* **Grant → tool projection** - each `Descriptor` becomes one MCP tool and
  one matching HTTP endpoint named `verb_resource`, its `inputSchema` derived
  (draft-07) from the grant's
  path/query/body, and its description taken from `describe` or derived as a
  user-goal sentence. mcp-beaver also derives a human title, read-only,
  destructive, idempotent, and open-world annotations from the operation's
  HTTP behavior, plus a `{coverage: ..., result: ...}` output schema.
  Initialization includes compact server instructions for the policy boundary.
  `internal/mcpserver`.
* **Coverage before payload** - every grant-backed result leads with a
  `coverage` block and carries the payload under `result`, in both the text and
  the structured content. A consuming harness bounds a tool result by keeping
  the front and discarding the tail, so a caveat serialized last is destroyed
  first and deterministically, and the model then answers from rows with no
  caveat. Coverage states `truncated` (always false, stated rather than
  omitted), `bytes`, `over_budget` past the smallest measured consumer cap, and
  `items` naming every array in the payload and its length - a count in meaning
  is what changes an answer, where a byte total does not. Enforced by the
  envelope being a struct rather than a map, so field order is a contract
  instead of an alphabetical accident. What the runtime cannot enforce - an
  upstream that leaves an array unbounded, or that answers a failed read with
  zero - is recorded in [DESIGN.md](DESIGN.md) rather than left implicit.
* **Typed query routing** - MCP query arguments retain their JSON types when
  mcp-beaver passes them to opcore. Scalars keep their existing wire spelling,
  arrays become repeated upstream keys in caller order, and type, bound,
  required-field, array-length, and mutual-exclusion violations fail before
  the upstream receives a request. Flat string query specs stay compatible.
* **Guarded execute** - a `tools/call` routes the MCP arguments onto
  `opcore.Args` (path/query → URL, body → JSON) without flattening nested body
  objects or arrays, then fires the self-guarding `opcore.Operation.Execute`:
  metachar gate, `restrict` allowlist, base-url, and env-token auth are the
  engine's, never re-implemented here. A denied or failed call returns as a
  tool result with `isError` set.
* **Built-in reader metadata** - the exact-parameter SSM tools advertise
  read-only, non-destructive, idempotent, open-world behavior and a specific
  structured parameter output schema. Passthrough mode preserves upstream
  titles, schemas, annotations, and results without reclassifying them.
* **Deny-by-absence** - the served surface is exactly the `can` grants. An
  unwritten grant is an absent tool; that is the deletion guard.

## `mcp-beaver lint <spec.mcp.kdl>`

The offline validation surface. It is `serve` minus the listener and minus
telemetry: read the spec, build the same server, print the minted tool names,
exit. No network, so it runs in a sealed clone and in CI.

* **Owning-loader validation** - lint builds the server through the same
  constructor `serve` uses rather than calling `opcore.ParseInline` directly,
  so it validates the grant-to-tool projection as well as the KDL parse. A
  well-formed file whose grants collide on one tool name fails here.
* **Tool-name output** - a clean spec exits 0 and writes the projected tool
  names to stdout, sorted, one per line. This is how a consumer repo reads its
  served surface off the owning loader instead of writing a second parser for
  the same guardfile. A rejected spec exits non-zero with the failure on
  stderr and writes nothing to stdout.
* **Warnings on stderr** - two facts that are invisible from every other
  surface, so a spec carrying one lints identically to a working spec. An
  unknown verb resolving to POST by fallthrough, and a `resource` stating no
  `audience`. Both are legitimate choices, so both warn rather than fail, and
  both keep off stdout so a warning never edits the diffable surface. The
  fallthrough warning reads opcore's own `MethodInferred`, so a grant that
  states `method` is owed no warning.
* **Stated HTTP method** - `method "POST"` inside a `can` body picks the method
  outright and leaves the verb free to name the tool well. The verb otherwise
  does both jobs, and they collide on a read served over POST - ordinary for a
  search API with a structured request body - which forced a create-shaped verb
  and a tool named `create_web_search` for a call that creates nothing. The
  verb table stays the default when `method` is absent. A stated `DELETE` marks
  the grant destructive whatever the verb is called, since the confirmation
  gate keys off the effect rather than the spelling, and anything outside
  GET / POST / PUT / PATCH / DELETE / HEAD fails closed. Grammar owned by
  umbra; pinned from v0.148.0.
* **Scope** - the `wrap` inline grammar the serving runtime renders.
  `serve-ssm` policies use a separate grammar and are not lintable through
  this path.

## Guardfile siblings: instructions, resources, prompts, server-info, PDF extraction, response cache, withheld verbs, confirmations

Top-level nodes stated beside `wrap`, outside the frozen inline grammar
`opcore.ParseInline` owns. Each fails closed on an unknown property or child.
All are opt-in except `server-info`, which is on by default and opts out.

* **Server instructions** - `instructions { text ... }` states what this server
  is for, published under the shared policy sentence in
  `InitializeResult.Instructions`. Every generated server used to publish only
  that shared sentence, so a client holding four of them learned that each
  exposes policy-approved tools - true of all four and distinguishing none. The
  policy floor still reaches every server and still serialises first. Bounded
  at 500 characters, because a consumer that renders this into the model's
  prompt pays for it every turn, once per rostered server. A guardfile that
  declares nothing publishes exactly what it published before.
* **Resources** - `resource "<name>" uri=... { text ... }` serves static
  content on `resources/read`. Inline only by design: a resource proxying an
  upstream read would be a second, unguarded egress path beside the grants.
  Claude Code surfaces these as `@` mentions. `audience "assistant"` and
  `priority=0.9` emit the MCP annotations an agent harness gates on when
  deciding to pull a resource into a model's context unprompted. Stating no
  audience means no such harness includes the resource, so `lint` warns:
  serving correctly and reaching nobody are the same thing from outside.
* **Prompts** - `prompt "<name>" { argument ...; text ... }` serves a message
  template on `prompts/get` with `{arg}` substitution. A missing required
  argument is an error, since a half-filled prompt reads as a complete one.
  Claude Code surfaces these as slash commands.
* **Server info** - one read-only tool reporting the server's identity, mode,
  and tool inventory. It reaches no upstream and restores the liveness probe
  2026-07-28 removed along with `ping`. **On by default**: every field it
  returns is already reachable through `initialize`, `tools/list`, and the
  list methods, so opting out withholds nothing, and a probe present on only
  some servers lets an agent read no meaning from its absence on the rest.
  `server-info name="status"` renames it, `server-info disabled` removes it.
  It counts itself, so `lint` and `tools/list` report the same surface.
* **Query pins** - `pin "<tool>" { query "<name>" env "<VAR>" }` fixes an
  outgoing query parameter server-side, resolved at call time from `env`,
  `file`, or `literal`. The pinned name is absent from the tool schema, so a
  caller can neither supply nor override it. This is the spec-mode counterpart
  to the proxy's `--pin`, and it exists because `set` writes fixed **body**
  values only, leaving a GET whose scope rides in the query string nowhere to
  put it. Pinning a parameter the grant also declares as a caller input is a
  build error, and an unresolvable pin fails the call rather than sending an
  unscoped request. The concrete case is Steam's `steamid`, where a declared
  field would turn "this account's library" into "any account's library".
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
* **Rate limit** - `rate-limit "1/1s"` states a per-server, process-wide
  outbound bucket in `<count>/<duration>` form. It serialises rather than
  rejecting: a queued tool call is slower, a 503 is a failed turn. The pod has
  one IP, so a public-good API otherwise sees one caller whose request rate is
  the sum of a whole community's traffic. Grant-backed tools only - the info
  tool and withheld stubs reach no upstream and are not charged. A call that
  would queue past the request deadline fails with a stated timeout rather than
  holding a slot.
* **PDF text extraction** - `extract "<tool-name>" as="pdf-text" max-pages="20"`
  turns a PDF an upstream returns into text an agent can read. A lot of
  authoritative reference material - government statistics, standards bodies,
  regulatory filings, equipment documentation - is published only as PDF, so a
  grant reaching one used to return bytes no model could use. Requires the
  grant to declare `raw-response`, checked at build. Bounded on three axes: a
  32MB size gate before the parser, a page bound defaulting to 20 and ceilinged
  at 200, and a parse that runs off the request goroutine so a wedged document
  returns a stated timeout rather than holding the handler. Malformed and
  encrypted documents are clean tool errors, and a parser panic is recovered
  into one. The coverage block gains `pages: {shown, total}`, so a bounded read
  cannot be mistaken for the whole document. Text only - table extraction and
  OCR are separate decisions. The in-process-versus-sidecar posture and the
  library choice are recorded in [DESIGN.md](DESIGN.md).
* **Response cache** - `cache "<tool-name>" ttl="15m"` reuses one grant's
  upstream answer for a window. **Not** the `ttlMs` / `cacheScope` on list
  results, which cache the tool inventory rather than the data. Opt-in per
  grant and off by default, because correctness varies entirely by upstream and
  a stale answer beats a slow one only where the author says so. Keyed on the
  tool plus its canonicalised arguments, so two spellings of the same object
  hit the same entry and two different questions never do. Sits outside the
  rate limiter, so a hit spends no slot - a rate limit bounds the rate and can
  only make a tool refuse, where a cache is the one control that lowers a
  metered bill without lowering capability. Failed calls are never stored,
  caching a destructive grant or a `confirm`-gated one is a build error, and
  the store is in-process and dies with the pod - the same objection #69 raises
  about the rate limiter, with the same answer if a durable store lands.
* **Withheld verbs** - `withhold "<tool-name>" { reason ...; alternative ... }`
  mints a discoverable stub for a verb left out on purpose. It appears in
  `tools/list`, states why in its description, names a substitute where one
  exists, and refuses every call with a structured `verb_withheld` payload
  while reaching no upstream. A `coilyco.io/withheld` marker in `_meta` lets a
  client separate stubs from live tools without reading prose. Absence
  otherwise means four different things and an agent has to guess which.
* **Confirmations** - `confirm "<tool-name>"` gates one tool behind a Multi
  Round-Trip Request: the first call returns an input request, and the tool
  runs only on a retry carrying an explicit accept. Anything else never
  reaches the upstream. Per tool rather than blanket, because these run
  headless where a prompt on every write would wedge the deployment.

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

## Request bounds

Nothing on this axis was bound before: `http.Server` was built with no
timeouts, the proxy client was nil (so `http.DefaultClient`, which has none),
and the SDK does not propagate HTTP cancellation to handlers by default. A
wedged upstream therefore held a request for as long as the caller would wait.

* **Per-call deadline** - `--request-timeout` (default 60s, 0 disables) bounds
  one tool call end to end, applied at the handler so it holds for the MCP
  transport and `POST /api/{tool-name}` alike, and for every protocol version.
  It rides the request context, so it aborts the outbound upstream call rather
  than only cutting the response.
* **Transport deadline** - the inbound request carries the per-call bound plus
  five seconds of headroom, so the tool always expires first and the runtime
  still has room to report the failure. Both expiring together produced an
  empty body, which reads as a crashed pod rather than a slow upstream.
* **Cancellation propagation** - a caller that goes away cancels the work.
  Applies to >= 2026-07-28 clients, which is why the per-call bound is not
  redundant with it.
* **Upstream client** - proxy mode bounds its own client at 45s when the caller
  supplied none, under the inbound deadline. A caller-set timeout is preserved.
  Spec mode was already bounded by opcore's 30s default client.
* **Connection guards** - 10s request-header timeout and a 120s idle timeout.
  No write timeout: it is absolute from the start of a request and would cut a
  legitimately slow upstream mid-response.
* **Attribution** - the MCP method, and the tool where there is one, are stamped
  on the transport span before dispatch, so a request that never returns still
  names what was in flight. `/healthz` is exempt from every deadline: a
  liveness probe a wedged upstream can fail turns one slow dependency into a
  restart loop.

## Operator HTTP

Non-MCP endpoints for runtime inspection and control. These are HTTP surfaces
for operators, not MCP tools:

* **Describe** - `GET /admin/describe` returns the loaded guardfile name/path,
  projected tool count, transport mode, upstream presence, and safe non-secret
  config facts.
* **Reload** - `POST /admin/reload` is explicit but currently restart-only. The
  runtime cannot safely hot-reload its guarded state in place, so the endpoint
  reports restart required instead.

Supported MCP methods: `initialize`, `notifications/initialized`,
`notifications/cancelled`, `ping`, `tools/list`, `tools/call`, `prompts/list`,
`prompts/get`, `resources/list`, `resources/read`, `resources/templates/list`,
and the 2026-07-28 `server/discover` and `subscriptions/listen`.

Resource and prompt support rides that generic MCP surface and adds no runtime
specific admin, lifecycle, reload, or control verb. That constraint is the one
to preserve as the surface grows: operator control stays on the `/admin`
endpoints above, off the protocol.

## Image

A single [`Dockerfile`](../Dockerfile) builds the one generic runtime image
(distroless, nonroot). The spec is mounted or COPYed in and named on the command
line - the same binary drives every `.mcp.kdl`. Building the image is a CI
consequence of a landed commit: the `publish` job in
[`.forgejo/workflows/ci.yml`](../.forgejo/workflows/ci.yml) builds the Dockerfile
on every push to `main` and publishes the private single-architecture image as
`forgejo.coilysiren.me/coilyco-flight-deck/mcp-beaver:<full-source-sha>`
(mount-not-bake, so one image serves every guardfile and publishes only when
runtime source changes). The source gate runs in the moving :release aos
dev-base image. The trusted deploy runner owns the package-write credential,
verifies the remote manifest, and hands the exact reference to deploy. Fleet
consumers use a separate read-only credential. See [ci.md](ci.md) for the gate
and publish walkthrough plus the Actions-unit enablement gotcha (mcp-beaver#10).

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

- [README.md](../README.md) - the pitch and the image + chart distribution.
- [DESIGN.md](DESIGN.md) - the spec → image pipeline, the interior-only scope, the safety model.
- [chart.md](chart.md) - the chart's templates, values reference, and runtime contract.
- [ci.md](ci.md) - the CI gate + publish pipeline and the Actions-unit enablement gotcha.
