# ward-mcp

**A cli-guard Guardfile, no handwritten code, becomes a Docker image that serves a working MCP over HTTP/SSE.**

ward-mcp renders a [cli-guard](https://github.com/coilysiren/cli-guard) Guardfile into a guarded MCP server, baked into an OCI image. One generic runtime, many guardfiles. No per-server Go, no per-server Dockerfile, no per-server MCP handler - and no per-tool input schema, because cli-guard's engine derives it from the OpenAPI operation.

The spec configures **only the image interior**: which upstream, which auth, which grants become which tools. The image serves MCP over SSE/streamable-HTTP (never stdio - these run as remote k3s pods reached by URL). ward-mcp has **no relation to the ward codebase**, and uses [cli-mcp](https://github.com/coilysiren/cli-mcp) as a code reference only, not a dependency.

The exposed MCP surface is exactly the guardfile's grants: an unwritten `delete issue` grant means no `delete_issue` tool can ever be served (**deny-by-absence**), and `restrict owner matches coilyco-*` bounds every path. Audit one small file, hand a write-capable MCP to an agent, know the blast radius.

## Quickstart

The generic `ward-mcp serve` runtime renders any `.mcp.kdl` into a guarded MCP
server. Run it directly:

```sh
# serve a spec over HTTP/SSE; the token is injected at run time via env
FORGEJO_TOKEN=... go run ./cmd/ward-mcp serve examples/forgejo-issues.mcp.kdl --http :8080

# list the derived tools (streamable-HTTP transport at /mcp)
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

The forgejo example serves `create_issue`, `get_issue`, `list_issue`, `comment_issue`, `close_issue` - each guarded, each scoped to `coilyco-*` / `kai` owners.

The runtime is a **thin shell** over cli-guard's [`http/opcore`](https://forgejo.coilysiren.me/coilyco-flight-deck/cli-guard) engine: `opcore.ParseInline` parses the spec, each grant projects to one MCP tool, and every call fires through the self-guarding `opcore.Operation.Execute` (metachar gate, `restrict`, auth). ward-mcp adds only the grant→tool projection and the MCP SSE/HTTP transport.

**Deploying** the image (k3s Service, route, tailnet-vs-public, token Secret) is out of scope here: it is consumed downstream as a [`deploy/services/`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) entry (deploy#40), the same way deploy already hosts `steam-mcp`, `reddit-mcp`, and `node-stats-mcp`.

## Layout

* [`cmd/ward-mcp`](cmd/ward-mcp) - the `serve` entrypoint: parse a spec, project tools, bind the HTTP/SSE listener.
* [`internal/mcpserver`](internal/mcpserver) - the thin shell: grant→MCP-tool projection, the JSON-RPC dispatch, and the streamable-HTTP + legacy-SSE transports.
* [`examples/forgejo-issues.mcp.kdl`](examples/forgejo-issues.mcp.kdl) - the worked "hello world": Forgejo issues as an MCP. Its body is the frozen ward-mcp inline grammar (`opcore.ParseInline`).
* [`examples/skillsmp.mcp.kdl`](examples/skillsmp.mcp.kdl) - the first end-to-end target: two read tools over SSE against skillsmp.com.
* [`docs/DESIGN.md`](docs/DESIGN.md) - the spec→image pipeline, the interior-only scope, and the HTTP/SSE + safety model.
* [`docs/FEATURES.md`](docs/FEATURES.md) - the living inventory of what ships today.

## Status

The `ward-mcp serve` runtime is **implemented** (ward-mcp#7): it parses a `.mcp.kdl`, serves the derived tools over MCP/SSE, and guarded-executes each call through opcore. Tracking [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept) and [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer). The chart that runs this image is ward-mcp#8.
