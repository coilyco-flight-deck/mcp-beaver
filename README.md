# mcp-beaver

A MCP server generator with a natural flow

![mcp-beaver // .mcp.kdl - A MCP server generator with a natural flow](assets/banner/mcp-beaver-banner.jpg)

mcp-beaver renders an [umbra](https://forgejo.coilysiren.me/coilyco-flight-deck/umbra)
guardfile into a guarded MCP server and HTTP tool API, baked into one generic
OCI image. One runtime, many guardfiles. No per-server Go, no per-server
Dockerfile, no per-server handler, and no per-tool input schema, because umbra
derives it from the inline operation definition in the `.mcp.kdl`.

You declare the operations an agent may reach, down to the leaf. Everything you
declared works and nothing else is reachable. An unwritten `delete issue` grant
means no `delete_issue` tool and no HTTP endpoint is ever served, and
`restrict owner matches coilyco-*` bounds every path. Audit one small file, hand
a write-capable MCP to an agent, and know the blast radius.

**The claim is about the running server, not the image.** The image is
deliberately generic and carries no guardfile, so a consumer mounts the spec at
deploy time. In spec mode an undeclared operation has no handler at all. In
upstream-proxy mode the undeclared tools still exist behind an endpoint the
container holds credentials for, and the runtime re-checks allowlist membership
on every call, which is unreachable rather than absent.

## Quickstart

```sh
FORGEJO_TOKEN=... go run ./cmd/mcp-beaver serve examples/forgejo-issues.mcp.kdl --http :8080

curl -s -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' localhost:8080/mcp
```

Every grant also projects at `POST /api/{tool-name}`, taking one JSON argument
object through the same handler and returning the MCP `CallToolResult` shape:

```sh
curl -s -H 'Content-Type: application/json' \
  -d '{"owner":"coilyco-flight-deck","repo":"mcp-beaver","index":"41"}' \
  localhost:8080/api/get_issue
```

[`examples/forgejo-issues.mcp.kdl`](examples/forgejo-issues.mcp.kdl) is the
worked hello-world, and its body is the whole contract.

## Four ways to serve

- **`serve`** reads a `.mcp.kdl` and guards an HTTP upstream. The general case.
- **`serve-upstream`** wraps a private MCP behind an exact tool allowlist, with
  `--connect-timeout` so a co-located upstream can warm up without putting the
  wrapper into a crash cycle. See [docs/upstream.md](docs/upstream.md).
- **`serve-ssm`** is the AWS SDK-backed exact-parameter reader. The policy names
  one parameter, and the general getter rejects every other name before AWS sees
  a request, with IAM bounding the principal independently. Two bounds rather
  than one. See [docs/ssm.md](docs/ssm.md).
- **`serve-s3`** is the asset publisher and the first write-capable mode. Its
  policy fixes one bucket, the media types it will serve, the public base URL,
  and an optional key prefix. See [docs/s3.md](docs/s3.md).

A guardfile may also name an API document with `spec <file>` and let umbra
resolve `can get repo` against it, and compose from another guardfile with
`inherit`. A tier can only narrow its base unless it writes `override` by name,
so a weaker surface is structural rather than asserted. `flatten` resolves the
composition into the one self-contained file a runtime mounts. See
[docs/spec-mode.md](docs/spec-mode.md).

`lint` and `lint-upstream` are the same paths minus the listener, for validating
a guardfile in CI. See [docs/lint.md](docs/lint.md).

## Results lead with coverage

Every grant-backed result is `{"coverage": {...}, "result": ...}`, in that
order, in both the text and the structured content. Coverage leads because a
consuming harness bounds a tool result by keeping the front and discarding the
tail, so a caveat serialized last is the first thing destroyed, and the model
then reads rows with no caveat and answers as though the view were complete.
Coverage names every array in the payload and its length, because a count in
meaning is what changes an answer. See [docs/DESIGN.md](docs/DESIGN.md).

## Authentication is the deployment's job

mcp-beaver performs no inbound authentication. Consuming deployments own caller
identity, TLS, ingress, and network reachability. Guardfile `auth` configures
mcp-beaver's credential to the upstream service, never the caller's credential
to mcp-beaver.

## Ships as image plus chart

```sh
helm upgrade --install skillsmp mcp-beaver \
  -f skillsmp.values.yaml \
  --set-file spec=skillsmp.mcp.kdl \
  --set image.tag=<built-runtime-sha>
```

Deploying an MCP is a values file plus `helm upgrade`. No per-guardfile image
build and no per-service manifest fork. The chart templates only the
auth-neutral runtime layer, and in spec mode it stays spec-opaque and never
parses the guardfile.
[`deploy`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) owns public
ingress, authentication, TLS, DNS, and rollout. See
[docs/chart.md](docs/chart.md).

Every push to canonical `main` publishes the runtime as
`forgejo.coilysiren.me/coilyco-flight-deck/mcp-beaver:<full-source-sha>`, and
the trusted publisher verifies the remote manifest.

## Naming

**A dam is not a wall. It decides what gets through.**

mcp-beaver shares no code with
[ward](https://forgejo.coilysiren.me/coilyco-flight-deck/ward), whose name this
project used to carry as `ward-mcp`. One `ward` spelling survives on purpose:
`wrap ward mcp <name>` opens every guardfile, because that line is umbra's
inline grammar rather than anything this runtime owns, and it moves when umbra
moves. [cli-mcp](https://github.com/coilysiren/cli-mcp) is a code reference
only, not a dependency.

## Brand assets

`assets/mark/` holds the mark and `assets/banner/` the banner and tile. All are
generated by `agentic-os-xxx`, whose `beaver-geometry.md` holds every number and
the reason for it. Edit those generators, never these files. There is no 16px
favicon, because five log ends cannot hold at that size.

## License

MIT. See [LICENSE](LICENSE).

## See also

- [AGENTS.md](AGENTS.md) - agent operating rules for this repository.
- [docs/FEATURES.md](docs/FEATURES.md) - what ships today.
- [docs/DESIGN.md](docs/DESIGN.md) - why it is shaped this way.
- [docs/guardfile-siblings.md](docs/guardfile-siblings.md) and [docs/guardfile-controls.md](docs/guardfile-controls.md) - the optional nodes stated beside `wrap`.
- [docs/telemetry.md](docs/telemetry.md) - opting into OpenTelemetry.
- [justfile](justfile) - dev verbs.
- [.ward/ward.yaml](.ward/ward.yaml) - catalog metadata only.
