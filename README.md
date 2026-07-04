# ward-mcp

**A cli-guard Guardfile, no handwritten code, becomes a Docker image running a working MCP server.**

ward-mcp is the MCP sibling of cli-guard's no-code `kdl-specs` CLI driver. Where `kdl-specs` renders a [cli-guard](https://github.com/coilysiren/cli-guard) Guardfile into a guarded urfave/cli binary, ward-mcp renders the **same guardfile** into a guarded MCP server, baked into an OCI image by CI. One generic runtime, many guardfiles. No per-server Go, no per-server Dockerfile, no per-server MCP handler - and no per-tool input schema, because the engine derives it from the OpenAPI operation.

ward-mcp has **no relation to the ward codebase.** It is a driver over cli-guard's spec engine and its [cli-mcp](https://github.com/coilysiren/cli-mcp) sibling.

The exposed MCP surface is exactly the guardfile's grants: `never delete issue` means no `delete_issue` tool can ever be served, and `restrict owner matches coily*` bounds every path. Audit one small file, hand a write-capable MCP to an agent, know the blast radius.

## Quickstart (drafted, not yet implemented)

```sh
# lock the granted surface (cli-guard's existing online step), then build the image
ward mcp build examples/forgejo-issues.guardfile.kdl

# run it, injecting the token at run time (never baked in)
docker run -i --rm -e FORGEJO_TOKEN forgejo.coilysiren.me/coilyco-flight-deck/ward-mcp-forgejo-issues
```

The container speaks MCP over stdio; add it to an agent's MCP config and it exposes `create_issue`, `get_issue`, `list_issues`, `comment_issue`, `close_issue` - each guarded, each scoped to `coily*` owners.

## Layout

* [`examples/forgejo-issues.guardfile.kdl`](examples/forgejo-issues.guardfile.kdl) - the worked "hello world": Forgejo issue creation as an MCP. It is a plain cli-guard Guardfile.
* [`docs/DESIGN.md`](docs/DESIGN.md) - the Guardfile -> image -> MCP pipeline, the safety model, and open questions.

## Status

Draft. Tracking [coilysiren/inbox#164](https://forgejo.coilysiren.me/coilysiren/inbox/issues/164) (concept) and [coilyco-bridge/deploy#40](https://forgejo.coilysiren.me/coilyco-bridge/deploy/issues/40) (first consumer).
