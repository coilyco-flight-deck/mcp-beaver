# ward-mcp

**A cli-guard Guardfile, no handwritten code, becomes a Docker image that serves a working MCP over HTTP.**

ward-mcp renders a [cli-guard](https://github.com/coilysiren/cli-guard) Guardfile into a guarded MCP server, baked into an OCI image. One generic runtime, many guardfiles. No per-server Go, no per-server Dockerfile, no per-server MCP handler - and no per-tool input schema, because cli-guard's engine derives it from the inline operation definition in the `.mcp.kdl`.

The spec configures **only the image interior**: which upstream, which auth, which grants become which tools. The image serves MCP over the official Go SDK's streamable HTTP transport at `/mcp` (never stdio - these run as remote k3s pods reached by URL). ward-mcp has **no relation to the ward codebase**, and uses [cli-mcp](https://github.com/coilysiren/cli-mcp) as a code reference only, not a dependency.

The exposed MCP surface is exactly the guardfile's grants: an unwritten `delete issue` grant means no `delete_issue` tool can ever be served (**deny-by-absence**), and `restrict owner matches coilyco-*` bounds every path. Audit one small file, hand a write-capable MCP to an agent, know the blast radius.

## Quickstart

The generic `ward-mcp serve` runtime renders any `.mcp.kdl` into a guarded MCP
server. Run it directly:

```sh
# initialize, then reuse the session id for tools/list and tools/call
FORGEJO_TOKEN=... go run ./cmd/ward-mcp serve examples/forgejo-issues.mcp.kdl --http :8080

# list the derived tools (SDK-backed streamable HTTP transport at /mcp)
curl -s -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' localhost:8080/mcp
```

Or as the image (one runtime, many specs - the spec is mounted, not baked):

```sh
docker build -t ward-mcp .
docker run -p 8080:8080 -e SKILLSMP_API_KEY \
  -v $PWD/examples/skillsmp.mcp.kdl:/spec/skillsmp.mcp.kdl \
  ward-mcp serve /spec/skillsmp.mcp.kdl --http :8080
```

For a passthrough MCP wrapper over a private upstream, use `serve-upstream`
with an allowlist. `--connect-timeout` lets a co-located upstream warm up
without putting the wrapper into a crash cycle:

```sh
ward-mcp serve-upstream --name grubhub-mcp \
  --upstream http://playwright-mcp.namespace.svc.cluster.local/mcp \
  --tool browser_navigate --tool browser_click \
  --connect-timeout 2m --http :8080
```

For an exact-parameter AWS SSM reader, use the KDL-backed SDK runtime:

```sh
ward-mcp serve-ssm /spec/aws-ssm.mcp.kdl --http :8080
```

The SSM policy declares one parameter and exactly two read tools. The general
getter accepts a name but rejects every value except the declared path. The
convenience getter fixes that same path internally.

The forgejo example serves `create_issue`, `get_issue`, `list_issue`, `comment_issue`, `close_issue` - each guarded, each scoped to `coilyco-*` / `kai` owners.

The runtime is a **thin shell** over cli-guard's [`http/opcore`](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard) engine: `opcore.ParseInline` parses the inline spec, including typed body blocks, typed and bounded query fields, repeated query arrays, mutually-exclusive query groups, and safe local aliases for upstream parameter names. Each grant projects to one MCP tool, and every call fires through the self-guarding `opcore.Operation.Execute` (metachar gate, `restrict`, auth). ward-mcp retains MCP JSON types when routing query arguments, so opcore validates the declared contract before an upstream call and serializes arrays as repeated keys in caller order. ward-mcp adds only the grant→tool projection and the SDK-backed transport/session layer.

## Distributes as image + chart

The product ships two artifacts (ward-mcp#6): the generic runtime **image** above, and a generic **Helm chart** (`chart/`) that templates the k3s exposure. **Deploying** an MCP is then a values file plus `helm upgrade` - no per-guardfile image build, no per-service manifest fork:

```sh
helm upgrade --install skillsmp ward-mcp \
  -f skillsmp.values.yaml \
  --set-file spec=skillsmp.mcp.kdl \
  --set image.tag=<built-runtime-sha>
```

The chart has two runtime modes. `spec` mounts a `.mcp.kdl` from chart values.
`upstream` runs `serve-upstream` with an exact tool allowlist and can co-locate
the private upstream through `extraContainers`. The chart templates only the
auth-neutral runtime layer: Deployment, Service, optional NodePort, and
application Secret wiring. In spec mode it stays **spec-opaque** and never
parses the guardfile. [`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy)
owns public ingress, authentication, TLS, DNS, and rollout.

## Layout

* [`cmd/ward-mcp`](cmd/ward-mcp) - the `serve` entrypoint: parse a spec, project tools, bind the SDK-backed HTTP listener.
* [`internal/mcpserver`](internal/mcpserver) - the thin shell: grant→MCP-tool projection, the SDK-backed streamable HTTP/session layer, and the non-MCP `/healthz` plus `/admin/*` operator endpoints.
* [`examples/forgejo-issues.mcp.kdl`](examples/forgejo-issues.mcp.kdl) - the worked "hello world": Forgejo issues as an MCP. Its body is the frozen ward-mcp inline grammar (`opcore.ParseInline`), and it is the whole contract.
* [`examples/skillsmp.mcp.kdl`](examples/skillsmp.mcp.kdl) - the first end-to-end target: two read tools over the SDK-backed transport against skillsmp.com.
* [`examples/*.values.yaml`](examples/) - reference auth-neutral chart values: `skillsmp` uses the default ClusterIP, and `forgejo-issues` demonstrates the optional NodePort.
* [`examples/upstream.values.yaml`](examples/upstream.values.yaml) - reference
  allowlisted upstream mode with a co-located MCP container.
* [`chart/`](chart/) - the generic ward-mcp Helm chart. See [`docs/chart.md`](docs/chart.md).
* [`Makefile`](Makefile) and [`.ward/ward.yaml`](.ward/ward.yaml) - the tracked development command surface.
* [`docs/DESIGN.md`](docs/DESIGN.md) - the spec→image pipeline, the interior-only scope, and the SDK-backed transport + safety model.
* [`docs/chart.md`](docs/chart.md) - the chart's templates, values reference, and the runtime contract it targets.
* [`docs/FEATURES.md`](docs/FEATURES.md) - the living inventory of what ships today.

## Status

The `ward-mcp serve` runtime is **implemented** (ward-mcp#7): it parses a `.mcp.kdl`, serves the derived tools over MCP at `/mcp`, guarded-executes each call through opcore, and exposes operator-only `/healthz` plus `/admin/describe` and `/admin/reload` HTTP endpoints outside the MCP tool surface. The generic Helm chart that runs this image is also in (ward-mcp#8). Tracking [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept) and [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).
