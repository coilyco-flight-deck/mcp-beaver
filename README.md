# ward-mcp

**A cli-guard Guardfile, no handwritten code, becomes a Docker image that serves a working MCP over HTTP/SSE.**

ward-mcp renders a [cli-guard](https://github.com/coilysiren/cli-guard) Guardfile into a guarded MCP server, baked into an OCI image. One generic runtime, many guardfiles. No per-server Go, no per-server Dockerfile, no per-server MCP handler - and no per-tool input schema, because cli-guard's engine derives it from the OpenAPI operation.

The spec configures **only the image interior**: which upstream, which auth, which grants become which tools. The image serves MCP over SSE/streamable-HTTP (never stdio - these run as remote k3s pods reached by URL). ward-mcp has **no relation to the ward codebase**, and uses [cli-mcp](https://github.com/coilysiren/cli-mcp) as a code reference only, not a dependency.

The exposed MCP surface is exactly the guardfile's grants: `never delete issue` means no `delete_issue` tool can ever be served, and `restrict owner matches coily*` bounds every path. Audit one small file, hand a write-capable MCP to an agent, know the blast radius.

## Quickstart (drafted, not yet implemented)

```sh
# lock the granted surface (cli-guard's existing online step), then build the image
ward mcp build examples/forgejo-issues.mcp.kdl

# run the image; it serves MCP over HTTP/SSE, token injected at run time
docker run -p 8080:8080 -e FORGEJO_TOKEN \
  forgejo.coilysiren.me/coilyco-flight-deck/ward-mcp-forgejo-issues \
  serve --http :8080
```

The image serves `create_issue`, `get_issue`, `list_issues`, `comment_issue`, `close_issue` - each guarded, each scoped to `coily*` owners.

**Deploying** the image (k3s Service, route, tailnet-vs-public, token Secret) is out of scope here: it is consumed downstream as a [`deploy/services/`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) entry (deploy#40), the same way deploy already hosts `steam-mcp`, `reddit-mcp`, and `node-stats-mcp`.

## Layout

* [`examples/forgejo-issues.mcp.kdl`](examples/forgejo-issues.mcp.kdl) - the worked "hello world": Forgejo issue creation as an MCP. Its body is a plain cli-guard Guardfile.
* [`docs/DESIGN.md`](docs/DESIGN.md) - the Guardfile -> image pipeline, the interior-only scope, the HTTP/SSE and safety model, and the remaining open question.

## Status

Draft. Tracking [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept) and [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).
