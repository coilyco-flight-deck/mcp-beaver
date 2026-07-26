# ward-mcp features

The living inventory of what ward-mcp ships today. Completes the
README / AGENTS / docs/FEATURES trifecta. ward-mcp turns a cli-guard Guardfile
into a guarded MCP server, distributed as a runtime image plus a generic Helm
chart.

## `ward-mcp serve <spec.mcp.kdl> --http :addr`

The generic local runtime. One static binary renders any `.mcp.kdl` spec into
a guarded MCP server over the official MCP Go SDK's streamable HTTP transport
at `/mcp`. No per-guardfile Go, no per-server handler. It never binds stdio -
these run as remote pods reached by URL.

* **Spec parse** - `opcore.ParseInline` (cli-guard `http/opcore`, pinned
  `v0.122.0`) parses the ward-mcp inline grammar: `wrap` header, `base-url`,
  `auth`, `restrict`, and each
  `can <verb> <resource> { path/query/body/set }` grant. Body blocks preserve
  JSON types and required fields. Query blocks preserve string, boolean,
  integer, number, and scalar-array types plus numeric bounds, array length
  bounds, required fields, mutually-exclusive groups, and safe local aliases
  for upstream parameter names. The `.mcp.kdl` is the whole contract. Method is
  inferred from the verb, path params from the `{template}`.
* **Grant → MCP tool projection** - each `Descriptor` becomes one tool named
  `verb_resource`, its `inputSchema` derived (draft-07) from the grant's
  path/query/body, its description the grant sentence. `internal/mcpserver`.
* **Typed query routing** - MCP query arguments retain their JSON types when
  ward-mcp passes them to opcore. Scalars keep their existing wire spelling,
  arrays become repeated upstream keys in caller order, and type, bound,
  required-field, array-length, and mutual-exclusion violations fail before
  the upstream receives a request. Flat string query specs stay compatible.
* **Guarded execute** - a `tools/call` routes the MCP arguments onto
  `opcore.Args` (path/query → URL, body → JSON) and fires the self-guarding
  `opcore.Operation.Execute`: metachar gate, `restrict` allowlist, base-url, and
  env-token auth are the engine's, never re-implemented here. A denied or failed
  call returns as a tool result with `isError` set.
* **Deny-by-absence** - the served surface is exactly the `can` grants. An
  unwritten grant is an absent tool; that is the deletion guard.

## `ward-mcp serve-upstream --upstream <mcp-url> --tool <name>...`

The guarded passthrough backend. It connects to a private streamable-HTTP MCP
upstream, snapshots the allowlisted upstream tool contracts, and exposes only
that subset on the outward MCP surface.

* **Upstream tool projection** - each allowlisted upstream tool becomes one
  outward MCP tool, preserving the upstream schema and tool metadata where
  possible.
* **Fail closed** - unknown upstream tools and schema drift return MCP tool
  errors instead of silently widening or mutating the surface.
* **Proxy calls** - allowed `tools/call` requests are forwarded to the upstream
  MCP session after the guard checks.
* **Bounded startup retry** - optional `--connect-timeout` retries the initial
  MCP connection while a co-located upstream starts. Zero retains fail-fast
  behavior for direct CLI use.

## `ward-mcp serve-ssm <spec.mcp.kdl> --http :addr`

The AWS SDK-backed exact-parameter reader. Its KDL policy names one parameter
and grants exactly `get_parameter(name)` plus `get_forgejo_read_token()`.
The general tool rejects every other name before AWS receives a request, while
IAM independently restricts the workload principal to the same parameter ARN.

## Transports

The runtime exposes the SDK-backed streamable HTTP transport and a liveness
probe:

* **Streamable HTTP** (`/mcp`, SDK-backed) - `initialize`, `tools/list`, and
  `tools/call` ride the MCP Go SDK's session lifecycle and session IDs.
* **Health** - `GET /healthz` for a pod liveness probe.

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
`notifications/cancelled`, `ping`, `tools/list`, `tools/call`. Any future
resource or prompt support must stay on the generic MCP surface
(`resources/list`, `resources/read`, `prompts/list`, `prompts/get`) and must not
grow Ward-specific admin, lifecycle, reload, or control verbs.

## Image

A single [`Dockerfile`](../Dockerfile) builds the one generic runtime image
(distroless, nonroot). The spec is mounted or COPYed in and named on the command
line - the same binary drives every `.mcp.kdl`. Building the image is a CI
consequence of a landed commit: the `publish` job in
[`.forgejo/workflows/ci.yml`](../.forgejo/workflows/ci.yml) builds the Dockerfile
on every push to `main` and pushes it to the in-cluster registry as
`ward-mcp:<sha>` (mount-not-bake, so one image serves every guardfile - published
when the runtime source changes, not per spec). The `gate` and `publish` jobs
run inside the moving :release aos dev-base image
(`forgejo.coilysiren.me/coilyco-flight-deck/agentic-os:release`), so the CI
surface no longer bootstraps Go or the Docker CLI itself. The deploy CD resolves
that sha into the chart's `image.tag` and rolls it. Mirrors the fleet's other
MCP source repos (reddit-mcp / node-stats-mcp); the plain-http push to the
insecure in-cluster registry needs no registry secret. See [ci.md](ci.md) for
the gate + publish walkthrough and the Actions-unit enablement gotcha
(ward-mcp#10).

## The generic Helm chart (`chart/`)

The auth-neutral distribution vehicle (ward-mcp#8). One chart, one runtime
image, many releases. `runtime.mode: spec` mounts a `.mcp.kdl` ConfigMap.
`runtime.mode: upstream` runs an exact passthrough allowlist, omits the
guardfile, and can co-locate a private MCP through `extraContainers`. The chart
templates the Deployment, ClusterIP or optional NodePort Service, application
Secret wiring, and startup protection for sidecar-backed proxies. See
[chart.md](chart.md).

* **exposure-neutral** - the chart contains no ingress controller, identity
  provider, authentication proxy, certificate issuer, DNS provider, or
  protected-resource metadata assumptions. A consuming deployment brings its
  own exposure layer.
* **secret wiring** - `secret` maps each `value env <VAR>` the guardfile names to
  an SSM parameter path (chart mints an ExternalSecret) or an existing Secret ref.
  Upstream mode can disable injection into ward-mcp while a co-located
  upstream consumes the generated Secret directly.

Deploying an MCP, including the host, exposure, authentication, TLS, DNS, and
rollout, is [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy)'s
job (deploy#40 / deploy#46 / deploy#61). Its shared
`charts/ingress-public-authed` chart owns the CoilyCo fleet gate. ward-mcp ships
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
