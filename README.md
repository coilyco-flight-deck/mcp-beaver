# ward-mcp

**A cli-guard Guardfile, no handwritten code, becomes a Docker image running an HTTP/SSE MCP server on k3s.**

ward-mcp renders a [cli-guard](https://github.com/coilysiren/cli-guard) Guardfile into a guarded MCP server, baked into an OCI image and run always-on on kai-server's k3s cluster. One generic runtime, many guardfiles. No per-server Go, no per-server Dockerfile, no per-server MCP handler - and no per-tool input schema, because cli-guard's engine derives it from the OpenAPI operation.

It is **exclusively** an SSE / streamable-HTTP server: the whole point is to run on k3s and be reached by URL, so stdio does not apply. ward-mcp has **no relation to the ward codebase**, and uses [cli-mcp](https://github.com/coilysiren/cli-mcp) as a code reference only, not a dependency.

The exposed MCP surface is exactly the guardfile's grants: `never delete issue` means no `delete_issue` tool can ever be served, and `restrict owner matches coily*` bounds every path. Audit one small file, hand a write-capable MCP to an agent, know the blast radius.

## Quickstart (drafted, not yet implemented)

```sh
# lock the granted surface (cli-guard's existing online step), then build the image
ward mcp build examples/forgejo-issues.mcp.kdl

# run it as an HTTP/SSE server, token injected at run time (never baked in)
docker run -p 8080:8080 -e FORGEJO_TOKEN \
  forgejo.coilysiren.me/coilyco-flight-deck/ward-mcp-forgejo-issues \
  serve --http :8080
```

On k3s it deploys as a [`deploy/services/`](https://forgejo.coilysiren.me/coilyco-bridge/deploy) entry (Dockerfile + Helm chart + kai-server bundle), tailnet-only for write-capable surfaces. An agent adds the pod's SSE URL to its MCP config and gets `create_issue`, `get_issue`, `list_issues`, `comment_issue`, `close_issue` - each guarded, each scoped to `coily*` owners.

## Layout

* [`examples/forgejo-issues.mcp.kdl`](examples/forgejo-issues.mcp.kdl) - the worked "hello world": Forgejo issue creation as an MCP. Its body is a plain cli-guard Guardfile.
* [`docs/DESIGN.md`](docs/DESIGN.md) - the Guardfile -> image -> k3s pipeline, the HTTP/SSE and safety model, and remaining open questions.

## Status

Draft. Tracking [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept) and [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).
