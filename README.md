# mcp-beaver

![mcp-beaver // .mcp.kdl - A MCP server generator with a natural flow](assets/banner/mcp-beaver-banner.jpg)

A MCP server generator with a natural flow

**A dam is not a wall. It decides what gets through.**

## About

mcp-beaver renders a [umbra](https://forgejo.coilysiren.me/coilyco-flight-deck/umbra) Guardfile into a guarded MCP server and HTTP tool API, baked into an OCI image. One generic runtime, many guardfiles. No per-server Go, no per-server Dockerfile, no per-server MCP or HTTP handler - and no per-tool input schema, because umbra's engine derives it from the inline operation definition in the `.mcp.kdl`.

The spec configures **only the image interior**: which upstream, which outbound auth, which grants become which tools. The image serves MCP over the official Go SDK's streamable HTTP transport at `/mcp` and automatically exposes each tool at `POST /api/{tool-name}` (never stdio - these run as remote k3s pods reached by URL). mcp-beaver shares **no code with [ward](https://forgejo.coilysiren.me/coilyco-flight-deck/ward)**, whose name this project used to carry as `ward-mcp`. One `ward` spelling survives on purpose: `wrap ward mcp <name>` opens every guardfile below, because that line is [umbra](https://forgejo.coilysiren.me/coilyco-flight-deck/umbra)'s inline grammar rather than anything this runtime owns, and it moves when umbra moves. [cli-mcp](https://github.com/coilysiren/cli-mcp) is a code reference only, not a dependency.

You declare the operations an agent may reach, down to the leaf. Everything you declared works. Nothing else is reachable. An unwritten `delete issue` grant means no `delete_issue` tool or HTTP endpoint is ever served (**deny-by-absence**), and `restrict owner matches coilyco-*` bounds every path. The one entry that is not a grant is `mcp_beaver_info`, a read-only tool that reports the server's own shape, reaches no upstream, and can be turned off with `server-info disabled`. Audit one small file, hand a write-capable MCP to an agent, know the blast radius.

The claim is about the running server, not the image. The image is deliberately generic: it carries no guardfile, and a consumer mounts the spec at deploy time. In upstream-proxy mode the undeclared tools exist behind an endpoint the container holds credentials for, and the runtime re-checks allowlist membership on every call - unreachable rather than absent. In spec mode an undeclared operation has no handler at all.

## Quickstart

The generic `mcp-beaver serve` runtime renders any `.mcp.kdl` into a guarded MCP
server. Each grant also projects ChatGPT-friendly metadata: a human title,
user-goal description, input and output schemas, and safety annotations derived
from the operation's HTTP behavior. Run it directly:

```sh
# initialize, then reuse the session id for tools/list and tools/call
FORGEJO_TOKEN=... go run ./cmd/mcp-beaver serve examples/forgejo-issues.mcp.kdl --http :8080

# list the derived tools (SDK-backed streamable HTTP transport at /mcp)
curl -s -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' localhost:8080/mcp

# call the same guarded handler without an MCP session
curl -s -H 'Content-Type: application/json' \
  -d '{"owner":"coilyco-flight-deck","repo":"mcp-beaver","index":"41"}' \
  localhost:8080/api/get_issue
```

Every grant-backed result is `{"coverage": {...}, "result": ...}`, in that
order, in both the text and the structured content. Coverage leads because a
consuming harness bounds a tool result by keeping the front and discarding the
tail, so a caveat serialized last is the first thing destroyed - and the model
then reads rows with no caveat and answers as though the view were complete. It
states `truncated` (always false; nothing here truncates), the payload's
`bytes`, whether that is `over_budget` for the smallest consumer cap measured
in the fleet, and `items` naming every array in the payload and its length. A
count in meaning is what changes an answer; a byte total is not. See
[docs/DESIGN.md](docs/DESIGN.md) for what the runtime enforces and what stays
the upstream's word.

The HTTP projection is always present. It accepts one JSON argument object,
uses the same tool handler as `tools/call`, and returns the MCP
`CallToolResult` JSON shape. mcp-beaver performs no inbound authentication.
Consuming deployments own caller identity, authentication, TLS, ingress, and
network reachability. Guardfile `auth` configures mcp-beaver's credential for the
upstream service, not the caller's credential to mcp-beaver.

Or as the image (one runtime, many specs - the spec is mounted, not baked):

```sh
docker build -t mcp-beaver .
docker run -p 8080:8080 -e SKILLSMP_API_KEY \
  -v $PWD/examples/skillsmp.mcp.kdl:/spec/skillsmp.mcp.kdl \
  mcp-beaver serve /spec/skillsmp.mcp.kdl --http :8080
```

For a passthrough MCP wrapper over a private upstream, use `serve-upstream`
with an allowlist. `--connect-timeout` lets a co-located upstream warm up
without putting the wrapper into a crash cycle:

```sh
mcp-beaver serve-upstream --name grubhub-mcp \
  --upstream http://playwright-mcp.namespace.svc.cluster.local/mcp \
  --tool browser_navigate --tool browser_click \
  --connect-timeout 2m --http :8080
```

A hosted upstream demanding a credential takes `--upstream-header`, whose
`{env:VAR}` span resolves in the container per request so the token never
reaches argv. See [docs/upstream.md](docs/upstream.md).

For an exact-parameter AWS SSM reader, use the KDL-backed SDK runtime:

```sh
mcp-beaver serve-ssm /spec/aws-ssm.mcp.kdl --http :8080
```

The SSM policy declares one parameter and exactly two read tools. The general
getter accepts a name but rejects every value except the declared path. The
convenience getter fixes that same path internally.

## Distributes as image + chart

The product ships two artifacts (mcp-beaver#6): the generic runtime **image** above, and a generic **Helm chart** (`chart/`) that templates the k3s exposure. **Deploying** an MCP is then a values file plus `helm upgrade` - no per-guardfile image build, no per-service manifest fork:

```sh
helm upgrade --install skillsmp mcp-beaver \
  -f skillsmp.values.yaml \
  --set-file spec=skillsmp.mcp.kdl \
  --set image.tag=<built-runtime-sha>
```

Every push to canonical `main` publishes the private single-architecture
runtime as
`forgejo.coilysiren.me/coilyco-flight-deck/mcp-beaver:<full-source-sha>`.
The trusted publisher verifies the remote manifest, and every fleet release
consumes that exact reference through a separate read-only credential.

The chart has two runtime modes. `spec` mounts a `.mcp.kdl` from chart values.
`upstream` runs `serve-upstream` with an exact tool allowlist and can co-locate
the private upstream through `extraContainers`. The chart templates only the
auth-neutral runtime layer: Deployment, Service, optional NodePort, and
application Secret wiring. In spec mode it stays **spec-opaque** and never
parses the guardfile. [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy)
owns public ingress, authentication, TLS, DNS, and rollout.

## Layout

* [`cmd/mcp-beaver`](cmd/mcp-beaver) - the `serve` entrypoint: parse a spec, project tools, bind the SDK-backed HTTP listener. `lint` is the same path minus the listener and telemetry, and `lint-upstream` is the allowlist counterpart for `serve-upstream`.
* [`internal/mcpserver`](internal/mcpserver) - the thin shell: grant→MCP-tool and HTTP endpoint projection, the SDK-backed streamable HTTP/session layer, and the non-MCP `/healthz` plus `/admin/*` operator endpoints.
* [`examples/forgejo-issues.mcp.kdl`](examples/forgejo-issues.mcp.kdl) - the worked "hello world": Forgejo issues as an MCP. Its body is the frozen mcp-beaver inline grammar (`opcore.ParseInline`), and it is the whole contract.
* [`examples/skillsmp.mcp.kdl`](examples/skillsmp.mcp.kdl) - the first end-to-end target: two read tools over the SDK-backed transport against skillsmp.com.
* [`examples/*.values.yaml`](examples/) - reference auth-neutral chart values: `skillsmp` uses the default ClusterIP, and `forgejo-issues` demonstrates the optional NodePort.
* [`examples/upstream.values.yaml`](examples/upstream.values.yaml) - reference
  allowlisted upstream mode with a co-located MCP container.
* [`examples/upstream-authed.values.yaml`](examples/upstream-authed.values.yaml) - the hosted counterpart: an off-cluster upstream reached with a Secret-backed credential.
* [`chart/`](chart/) - the generic mcp-beaver Helm chart. See [`docs/chart.md`](docs/chart.md).
* [`.ward/ward.yaml`](.ward/ward.yaml) and [`scripts/ward-command.sh`](scripts/ward-command.sh) - the tracked development command surface.
* [`docs/DESIGN.md`](docs/DESIGN.md) - the spec→image pipeline, the interior-only scope, and the SDK-backed transport + safety model.
* [`docs/chart.md`](docs/chart.md) - the chart's templates, values reference, and the runtime contract it targets.
* [`docs/FEATURES.md`](docs/FEATURES.md) - the living inventory of what ships today.

## Status

The `mcp-beaver serve` runtime is **implemented** (mcp-beaver#7): it parses a `.mcp.kdl`, serves the derived tools over MCP at `/mcp` and HTTP at `/api/{tool-name}`, guarded-executes both projections through the same handler, and exposes operator-only `/healthz` plus `/admin/describe` and `/admin/reload` endpoints. The generic Helm chart that runs this image is also in (mcp-beaver#8). Tracking [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept) and [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).

## License

MIT. See [LICENSE](LICENSE).

## Brand assets

`assets/mark/` holds the mark (canon SVG, coin rasters at 400/256/128, favicons
at 64 and 32) and `assets/banner/` the 2:1 banner and 2.6:1 tile at 1x and 2x.
All generated by `agentic-os-xxx`, whose `beaver-geometry.md` holds every number
and the reason for it. Edit those generators, never these files. There is no
16px favicon: five log ends cannot hold at that size.

## Authoring guardfiles

The authoring guidance lives in `docs/`:

- [lint.md](docs/lint.md) - validating a guardfile, and stating a method.
- [guardfile-siblings.md](docs/guardfile-siblings.md) and
  [guardfile-controls.md](docs/guardfile-controls.md) - the optional nodes
  stated beside `wrap`.
- [upstream.md](docs/upstream.md) and
  [upstream-pins.md](docs/upstream-pins.md) - allowlists, scoping, and
  upstream credentials.
- [telemetry.md](docs/telemetry.md) - opting into OpenTelemetry.

## See also

- [AGENTS.md](AGENTS.md) - agent operating rules for this repository.
- [docs/FEATURES.md](docs/FEATURES.md) - what ships today.
- [docs/DESIGN.md](docs/DESIGN.md) - why it is shaped this way.
- [justfile](justfile) - dev verbs.
- [.ward/ward.yaml](.ward/ward.yaml) - catalog metadata only.
