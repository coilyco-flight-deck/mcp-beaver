# ward-mcp features

The living inventory of what ward-mcp ships today. Completes the
README / AGENTS / docs/FEATURES trifecta. ward-mcp turns a cli-guard Guardfile
into a guarded MCP server, distributed as a runtime image plus a generic Helm
chart.

## `ward-mcp serve <spec.mcp.kdl> --http :addr`

The generic runtime, and the only command. One static binary renders any
`.mcp.kdl` spec into a guarded MCP server over HTTP/SSE. No per-guardfile Go, no
per-server handler. It never binds stdio - these run as remote pods reached by
URL.

* **Spec parse** - `opcore.ParseInline` (cli-guard `http/opcore`, pinned
  `v0.80.0`) parses the frozen ward-mcp inline grammar: `wrap` header, `base-url`,
  `auth`, `restrict`, and each `can <verb> <resource> { path/query/body/set }`
  grant. The `.mcp.kdl` is the whole contract. Method is inferred from the verb,
  path params from the `{template}`.
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
consequence of a landed commit: the `publish` job in
[`.forgejo/workflows/ci.yml`](../.forgejo/workflows/ci.yml) builds the Dockerfile
on every push to `main` and pushes it to the in-cluster registry as
`ward-mcp:<sha>` (mount-not-bake, so one image serves every guardfile - published
when the runtime source changes, not per spec). The `gate` and `publish` jobs
run inside the pinned aos dev-base image
(`forgejo.coilysiren.me/coilyco-flight-deck/agentic-os:v0.195.0`), so the CI
surface no longer bootstraps Go or the Docker CLI itself. The deploy CD resolves
that sha into the chart's `image.tag` and rolls it. Mirrors the fleet's other
MCP source repos (reddit-mcp / node-stats-mcp); the plain-http push to the
insecure in-cluster registry needs no registry secret. See [ci.md](ci.md) for
the gate + publish walkthrough and the Actions-unit enablement gotcha
(ward-mcp#10).

## The generic Helm chart (`chart/`)

The distribution vehicle (ward-mcp#8). One chart, one runtime image, many
releases: the `.mcp.kdl` rides in as values (a ConfigMap), the chart templates
the k3s exposure (Deployment, Service, token Secret wiring, and the optional
Authelia-JWT public route). Adding an MCP is a values file + `helm upgrade`.
Spec-opaque, so the interior-only scope of the spec holds. See
[chart.md](chart.md).

* **public vs tailnet-only** - `route.public` toggles the full deploy#30 Authelia
  overlay (Ingress + ForwardAuth + oauth2-proxy + RFC 9728 metadata sidecar)
  against a tailnet-only NodePort. A write surface stays tailnet-only; a read
  surface can go public-gated.
* **ESO-generated cookie-secret** - the oauth2-proxy cookie-secret is generated
  in-cluster and preserved across `helm upgrade`, so a public deploy needs no
  hand-created SSM param and re-rolls do not force a re-login. Only the
  client-secret (the shared Authelia hash by default) stays SSM-backed.
* **secret wiring** - `secret` maps each `value env <VAR>` the guardfile names to
  an SSM parameter path (chart mints an ExternalSecret) or an existing Secret ref.

Deploying an MCP - picking the host, the SSM token, and public-vs-tailnet, and
rolling it out - is [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy)'s
job (deploy#40 / deploy#46 / deploy#61), which consumes the chart per-MCP with
one values file. ward-mcp ships the generic chart; deploy owns the values.

## Examples

* [`examples/forgejo-issues.mcp.kdl`](../examples/forgejo-issues.mcp.kdl) - the
  worked "hello world": five guarded issue tools scoped to `coilyco-*` / `kai`.
* [`examples/skillsmp.mcp.kdl`](../examples/skillsmp.mcp.kdl) - the first
  end-to-end target: two read tools over SSE against skillsmp.com.
* [`examples/*.values.yaml`](../examples/) - reference deploy-side chart values: a
  public Authelia-gated read (`skillsmp`) and a tailnet-only write
  (`forgejo-issues`).

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
